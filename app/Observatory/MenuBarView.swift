import AppKit
import SwiftUI

struct ObservatoryMenuBarView: View {
    @Environment(EngineClient.self) private var engine
    @Environment(\.openWindow) private var openWindow

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
