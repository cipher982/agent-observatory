import Foundation
import NetworkExtension
import SystemExtensions
import Observation
import os

private let log = Logger(subsystem: "com.github.cipher982.agentobservatory", category: "proxyctl")

// ProxyController owns the NetworkExtension lifecycle from the host app side:
// activate/deactivate the system extension (OSSystemExtensionRequest) and
// load/save/start the transparent-proxy configuration (NETransparentProxyManager).
//
// This is the NE capture ingress — it replaces the global HTTPS_PROXY env-var
// install. The system extension routes only allowlisted provider flows to the
// local Go MITM proxy; everything else flows normally by construction.
@MainActor
@Observable
final class ProxyController: NSObject {

    enum Status: Equatable {
        case unknown
        case inactive
        case activating
        case needsApproval        // user must approve in System Settings
        case active
        case failed(String)
    }

    private(set) var status: Status = .unknown
    private let extensionBundleID = "com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension"

    // MARK: System extension activation

    func activate() {
        status = .activating
        let req = OSSystemExtensionRequest.activationRequest(
            forExtensionWithIdentifier: extensionBundleID, queue: .main)
        req.delegate = self
        OSSystemExtensionManager.shared.submitRequest(req)
    }

    func deactivate() {
        let req = OSSystemExtensionRequest.deactivationRequest(
            forExtensionWithIdentifier: extensionBundleID, queue: .main)
        req.delegate = self
        OSSystemExtensionManager.shared.submitRequest(req)
        removeCATrust()
        Task { await stopTunnel() }
    }

    // MARK: Transparent proxy configuration

    private func configureAndStart() {
        let bundleID = extensionBundleID
        NETransparentProxyManager.loadAllFromPreferences { managers, error in
            if let error {
                Task { @MainActor in self.status = .failed("load preferences: \(error.localizedDescription)") }
                return
            }
            let manager = managers?.first ?? NETransparentProxyManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = bundleID
            proto.serverAddress = "127.0.0.1"   // must be non-nil; loopback is fine
            manager.protocolConfiguration = proto
            manager.localizedDescription = "Agent Observatory"
            manager.isEnabled = true
            manager.saveToPreferences { saveErr in
                if let saveErr {
                    Task { @MainActor in self.status = .failed("save preferences: \(saveErr.localizedDescription)") }
                    return
                }
                // Reload so the connection reference is valid after save.
                manager.loadFromPreferences { _ in
                    do {
                        try manager.connection.startVPNTunnel()
                        // Trust the local CA in the login keychain so agents accept
                        // the proxy's leaf certs — gated behind this approved sysext,
                        // and reversed by `agents trust remove` on uninstall.
                        Task { @MainActor in
                            self.installCATrust()
                            self.status = .active
                        }
                        log.log("transparent proxy started")
                    } catch {
                        Task { @MainActor in self.status = .failed("start tunnel: \(error.localizedDescription)") }
                    }
                }
            }
        }
    }

    // Invoke the bundled `agents` helper to add/remove the local CA's login-keychain
    // trust. The Security framework prompts the user once to authorize the change.
    private func installCATrust() { runHelperTrust("install") }
    func removeCATrust() { runHelperTrust("remove") }

    private func runHelperTrust(_ action: String) {
        guard let helper = Bundle.main.url(forResource: "agents", withExtension: nil) else {
            log.error("bundled agents helper not found; cannot \(action) CA trust")
            return
        }
        let p = Process()
        p.executableURL = helper
        p.arguments = ["trust", action]
        do { try p.run() } catch {
            log.error("agents trust \(action) failed to launch: \(error.localizedDescription)")
        }
    }

    private func stopTunnel() async {
        await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
            NETransparentProxyManager.loadAllFromPreferences { managers, _ in
                managers?.first?.connection.stopVPNTunnel()
                cont.resume()
            }
        }
    }
}

// MARK: - OSSystemExtensionRequestDelegate

extension ProxyController: OSSystemExtensionRequestDelegate {

    nonisolated func request(_ request: OSSystemExtensionRequest,
                             actionForReplacingExtension existing: OSSystemExtensionProperties,
                             withExtension ext: OSSystemExtensionProperties) -> OSSystemExtensionRequest.ReplacementAction {
        .replace
    }

    nonisolated func requestNeedsUserApproval(_ request: OSSystemExtensionRequest) {
        Task { @MainActor in
            self.status = .needsApproval
            log.log("system extension needs user approval in System Settings")
        }
    }

    nonisolated func request(_ request: OSSystemExtensionRequest,
                             didFinishWithResult result: OSSystemExtensionRequest.Result) {
        Task { @MainActor in
            switch result {
            case .completed:
                self.configureAndStart()
            case .willCompleteAfterReboot:
                self.status = .failed("activation completes after reboot")
            @unknown default:
                self.status = .failed("unknown activation result")
            }
        }
    }

    nonisolated func request(_ request: OSSystemExtensionRequest, didFailWithError error: any Error) {
        Task { @MainActor in
            self.status = .failed(error.localizedDescription)
            log.error("system extension request failed: \(error.localizedDescription)")
        }
    }
}
