import Foundation
import NetworkExtension
import SystemExtensions
import Observation
import os

private let log = Logger(subsystem: "com.github.cipher982.agentobservatory", category: "proxyctl")

// ProxyController owns the NetworkExtension lifecycle from the host app side:
// activate the system extension (OSSystemExtensionRequest), then load/save/start
// or stop the transparent-proxy configuration (NETransparentProxyManager).
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
        // Disable live capture without uninstalling the System Extension. Asking
        // sysextd to deactivate the extension leaves macOS in a
        // "terminated waiting to uninstall on reboot" state, which makes the
        // normal off/on loop feel broken. The safe user-facing kill switch is:
        // stop and disable the transparent-proxy manager, then remove CA trust.
        // The approved extension remains installed and can be started again
        // without reboot.
        //
        // Stop the tunnel FIRST, then remove CA trust — otherwise there's a
        // window where the proxy is still intercepting but agents no longer
        // trust its CA, which would fail their TLS handshakes instead of
        // cleanly passing through.
        Task {
            let disabled = await stopAndDisableTunnel()
            let trustRemoved = await self.removeCATrust()
            if disabled && trustRemoved {
                self.status = .inactive
            } else {
                self.status = .failed("Disable capture did not complete cleanly; run the reset action or bundled uninstall.")
            }
        }
    }

    func resetCaptureConfiguration() {
        Task {
            let removed = await removeTunnelConfiguration()
            let trustRemoved = await self.removeCATrust()
            if removed && trustRemoved {
                self.status = .inactive
            } else {
                self.status = .failed("Reset capture config failed; try quitting and relaunching Agent Observatory from /Applications.")
            }
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
                    // Install CA trust BEFORE starting the tunnel. Otherwise there's
                    // a window where the proxy intercepts provider flows but agents
                    // don't yet trust the forged cert, breaking their handshakes.
                    Task { @MainActor in
                        guard await self.installCATrust() else {
                            self.status = .failed("CA trust install failed; agents won't accept the proxy")
                            return
                        }
                        CapturePauseGate.clear()
                        do {
                            try manager.connection.startVPNTunnel()
                            log.log("transparent proxy started (CA trust already installed)")
                            self.status = .activating
                            self.confirmTunnelConnected(manager)
                        } catch {
                            self.status = .failed("start tunnel: \(error.localizedDescription)")
                        }
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

    private func confirmTunnelConnected(_ manager: NETransparentProxyManager) {
        Task { @MainActor in
            let deadline = Date().addingTimeInterval(8)
            while Date() < deadline {
                switch manager.connection.status {
                case .connected:
                    self.status = .active
                    return
                case .disconnecting, .disconnected, .invalid:
                    break
                case .connecting, .reasserting:
                    break
                @unknown default:
                    break
                }
                try? await Task.sleep(nanoseconds: 250_000_000)
            }
            self.status = .failed("Capture extension was approved, but the tunnel did not report connected. Refresh and try again.")
        }
    }

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

    private func stopAndDisableTunnel() async -> Bool {
        let bundleID = extensionBundleID
        return await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
            NETransparentProxyManager.loadAllFromPreferences { managers, error in
                if let error {
                    log.error("load preferences before disable failed: \(error.localizedDescription, privacy: .public)")
                    cont.resume(returning: false)
                    return
                }
                // Stop only OUR manager — never another transparent proxy the user
                // may have configured (matched the same way as configureAndStart).
                let ours = managers?.first {
                    ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
                }
                guard let ours else {
                    cont.resume(returning: true)
                    return
                }
                ours.connection.stopVPNTunnel()
                guard ours.isEnabled else {
                    cont.resume(returning: true)
                    return
                }
                ours.isEnabled = false
                ours.saveToPreferences { saveErr in
                    if let saveErr {
                        log.error("disable transparent proxy preferences failed: \(saveErr.localizedDescription, privacy: .public)")
                        cont.resume(returning: false)
                        return
                    }
                    log.log("transparent proxy manager disabled")
                    cont.resume(returning: true)
                }
            }
        }
    }

    private func removeTunnelConfiguration() async -> Bool {
        let bundleID = extensionBundleID
        return await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
            NETransparentProxyManager.loadAllFromPreferences { managers, error in
                if let error {
                    log.error("load preferences before reset failed: \(error.localizedDescription, privacy: .public)")
                    cont.resume(returning: false)
                    return
                }
                let ours = managers?.first {
                    ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
                }
                guard let ours else {
                    cont.resume(returning: true)
                    return
                }
                ours.connection.stopVPNTunnel()
                ours.removeFromPreferences { removeErr in
                    if let removeErr {
                        log.error("remove transparent proxy preferences failed: \(removeErr.localizedDescription, privacy: .public)")
                        cont.resume(returning: false)
                        return
                    }
                    log.log("transparent proxy manager removed")
                    cont.resume(returning: true)
                }
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
        let message = Self.activationFailureMessage(error)
        Task { @MainActor in
            self.status = .failed(message)
            log.error("system extension request failed: \(message, privacy: .public)")
        }
    }

    // Map OSSystemExtension errors to actionable guidance instead of the opaque
    // framework string (e.g. "Unable to find any matched extension…").
    nonisolated static func activationFailureMessage(_ error: any Error) -> String {
        ActivationErrorFormatter.message(for: error)
    }
}
