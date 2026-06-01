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

    var isActive: Bool { status == .active }

    // Reflect the current transparent-proxy state into `status` on launch, so the
    // UI shows "active" when capture is already running from a previous session
    // (the system extension and its config persist across app launches).
    func refreshStatus() {
        let bundleID = extensionBundleID
        NETransparentProxyManager.loadAllFromPreferences { managers, _ in
            let ours = managers?.first {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
            }
            Task { @MainActor in
                switch ours?.connection.status {
                case .connected:
                    self.status = .active
                case .connecting, .reasserting:
                    self.status = .activating
                case .disconnecting, .disconnected, .invalid, .none:
                    if self.status != .needsApproval && self.status != .activating {
                        self.status = .inactive
                    }
                @unknown default:
                    break
                }
            }
        }
    }

    // MARK: System extension activation

    // The NE relay forwards allowlisted flows to the Go proxy on 127.0.0.1:7879,
    // which terminates TLS with the STABLE CA created by `agents install`. The CA
    // we trust in the login keychain is that same stable CA. So NE capture is only
    // coherent when the installed launchd daemon (stable-CA proxy) is running — NOT
    // an app-spawned ephemeral-CA monitor. Gate activation on that precondition so
    // we never route agent TLS into a proxy presenting an untrusted cert.
    func activate() {
        Task { @MainActor in
            guard await self.installedDaemonPresent() else {
                self.status = .failed("Run `agents install` first — NE capture needs the installed daemon's stable CA.")
                return
            }
            self.status = .activating
            let req = OSSystemExtensionRequest.activationRequest(
                forExtensionWithIdentifier: self.extensionBundleID, queue: .main)
            req.delegate = self
            OSSystemExtensionManager.shared.submitRequest(req)
        }
    }

    // True when `agents status` reports a full install (stable CA on disk +
    // daemon), via the bundled helper.
    private func installedDaemonPresent() async -> Bool {
        guard let helper = Bundle.main.url(forResource: "agents", withExtension: nil) else { return false }
        return await Task.detached {
            let p = Process()
            p.executableURL = helper
            p.arguments = ["status"]
            let pipe = Pipe(); p.standardOutput = pipe; p.standardError = pipe
            do {
                try p.run(); p.waitUntilExit()
                let out = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                return out.contains("overall: installed")
            } catch { return false }
        }.value
    }

    func deactivate() {
        let req = OSSystemExtensionRequest.deactivationRequest(
            forExtensionWithIdentifier: extensionBundleID, queue: .main)
        req.delegate = self
        OSSystemExtensionManager.shared.submitRequest(req)
        // Stop the tunnel FIRST, then remove CA trust — otherwise there's a window
        // where the proxy is still intercepting but agents no longer trust its CA,
        // which would fail their TLS handshakes instead of cleanly passing through.
        Task {
            await stopTunnel()
            await self.removeCATrust()
        }
    }

    // MARK: Transparent proxy configuration

    private func configureAndStart() {
        let bundleID = extensionBundleID
        NETransparentProxyManager.loadAllFromPreferences { managers, error in
            if let error {
                Task { @MainActor in self.status = .failed("load preferences: \(error.localizedDescription)") }
                return
            }
            // Reuse only OUR manager (matched by provider bundle id), not whatever
            // happens to be first — another transparent proxy config may exist.
            let manager = managers?.first {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
            } ?? NETransparentProxyManager()
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
                        log.log("transparent proxy started")
                        // Install CA trust and only mark .active once it actually
                        // succeeds — otherwise the UI says "active" while agent TLS
                        // handshakes fail because the CA isn't trusted yet.
                        Task { @MainActor in
                            let ok = await self.installCATrust()
                            self.status = ok ? .active
                                : .failed("CA trust install failed; agents won't accept the proxy")
                        }
                    } catch {
                        Task { @MainActor in self.status = .failed("start tunnel: \(error.localizedDescription)") }
                    }
                }
            }
        }
    }

    // Invoke the bundled `agents` helper to add/remove the local CA's login-keychain
    // trust. The Security framework prompts the user once to authorize the change.
    // Returns true only if the helper exits 0, so callers can gate UI on real success.
    @discardableResult
    private func installCATrust() async -> Bool { await runHelperTrust("install") }
    @discardableResult
    func removeCATrust() async -> Bool { await runHelperTrust("remove") }

    private func runHelperTrust(_ action: String) async -> Bool {
        guard let helper = Bundle.main.url(forResource: "agents", withExtension: nil) else {
            log.error("bundled agents helper not found; cannot \(action) CA trust")
            return false
        }
        return await Task.detached {
            let p = Process()
            p.executableURL = helper
            p.arguments = ["trust", action]
            do {
                try p.run()
                p.waitUntilExit()
                return p.terminationStatus == 0
            } catch {
                log.error("agents trust \(action) failed to launch: \(error.localizedDescription)")
                return false
            }
        }.value
    }

    private func stopTunnel() async {
        let bundleID = extensionBundleID
        await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
            NETransparentProxyManager.loadAllFromPreferences { managers, _ in
                // Stop only OUR manager — never another transparent proxy the user
                // may have configured (matched the same way as configureAndStart).
                let ours = managers?.first {
                    ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
                }
                ours?.connection.stopVPNTunnel()
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
