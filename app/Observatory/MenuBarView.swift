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

        Text(engine.mode == .demo ? "Demo mode" : "Live mode")
        Text(engine.streamConnected ? "Stream connected" : "Stream reconnecting")

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
