import Foundation
import NetworkExtension

enum CaptureResetCommand {
    static let flag = "--reset-capture-config-and-exit"
    private static let extensionBundleID = "com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension"

    static func runIfRequested() {
        guard CommandLine.arguments.contains(flag) else { return }
        let configRemoved = removeTunnelConfiguration()
        let trustRemoved = runHelperTrustRemove()
        if configRemoved && trustRemoved {
            FileHandle.standardOutput.write(Data("capture reset: removed Observatory capture config and CA trust\n".utf8))
            Foundation.exit(0)
        }
        FileHandle.standardError.write(Data("capture reset: failed to fully remove Observatory capture config or CA trust\n".utf8))
        Foundation.exit(1)
    }

    private static func removeTunnelConfiguration() -> Bool {
        let deadline = Date().addingTimeInterval(30)
        var done = false
        var ok = true

        NETransparentProxyManager.loadAllFromPreferences { managers, error in
            if let error {
                FileHandle.standardError.write(Data("capture reset: load preferences failed: \(error.localizedDescription)\n".utf8))
                ok = false
                done = true
                return
            }
            let ours = managers?.first {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == extensionBundleID
            }
            guard let ours else {
                done = true
                return
            }
            ours.connection.stopVPNTunnel()
            ours.removeFromPreferences { removeError in
                if let removeError {
                    FileHandle.standardError.write(Data("capture reset: remove preferences failed: \(removeError.localizedDescription)\n".utf8))
                    ok = false
                }
                done = true
            }
        }

        while !done && Date() < deadline {
            RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.1))
        }
        if !done {
            FileHandle.standardError.write(Data("capture reset: timed out removing preferences\n".utf8))
            return false
        }
        return ok
    }

    private static func runHelperTrustRemove() -> Bool {
        guard let helper = Bundle.main.url(forResource: "agents", withExtension: nil) else {
            FileHandle.standardError.write(Data("capture reset: bundled agents helper not found\n".utf8))
            return false
        }
        let process = Process()
        process.executableURL = helper
        process.arguments = ["trust", "remove"]
        do {
            try process.run()
            process.waitUntilExit()
            return process.terminationStatus == 0
        } catch {
            FileHandle.standardError.write(Data("capture reset: agents trust remove failed to launch: \(error.localizedDescription)\n".utf8))
            return false
        }
    }
}
