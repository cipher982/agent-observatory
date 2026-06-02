import AppKit
import SwiftUI

struct ObservatoryMenuBarView: View {
    @Environment(EngineClient.self) private var engine
    @Environment(ProxyController.self) private var proxy
    @Environment(\.openWindow) private var openWindow
    @AppStorage("observatory.onboarding.completed") private var onboardingCompleted = false
    @AppStorage("observatory.onboarding.visible") private var onboardingVisible = true

    var body: some View {
        Button("Show Agent Observatory") {
            openWindow(id: "main")
            NSApplication.shared.activate()
        }

        Divider()

        Text(engine.mode == .demo ? "Demo ready" : "Live capture")
        Text(engine.streamConnected ? "Connected" : "Reconnecting")

        Text(proxy.isActive ? "Capture extension: on" : "Capture extension: off")

        // Always-available capture kill switch / enable. This stops the
        // transparent-proxy tunnel and removes CA trust without uninstalling the
        // approved system extension, so users can turn capture back on without a
        // reboot.
        Button(proxy.isActive ? "Disable Live Capture" : "Enable Live Capture") {
            if proxy.isActive {
                proxy.deactivate()
            } else {
                proxy.activate()
            }
        }
        .disabled(proxy.status == .activating)

        Button("Reset Capture Config") {
            proxy.resetCaptureConfiguration()
        }
        .disabled(proxy.status == .activating)

        Button(engine.mode == .demo ? "Switch to Live Mode" : "Switch to Demo Mode") {
            engine.restart(mode: engine.mode == .demo ? .live : .demo)
        }

        Button("Show Onboarding") {
            onboardingCompleted = false
            onboardingVisible = true
            if engine.mode != .demo {
                engine.restart(mode: .demo)
            }
            openWindow(id: "main")
            NSApplication.shared.activate()
        }

        Button("Refresh Sessions") {
            Task { await engine.refresh() }
        }

        Divider()

        Button("Quit Agent Observatory") {
            NSApplication.shared.terminate(nil)
        }
        .keyboardShortcut("q")
    }
}
