import AppKit
import SwiftUI

struct ObservatoryMenuBarView: View {
    @Environment(EngineClient.self) private var engine
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
