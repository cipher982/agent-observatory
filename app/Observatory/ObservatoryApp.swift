import SwiftUI

@main
struct ObservatoryApp: App {
    @State private var engine = EngineClient(apiPort: 7878, proxyPort: 7879)

    var body: some Scene {
        WindowGroup("Agent Observatory", id: "main") {
            ContentView()
                .environment(engine)
                .frame(minWidth: 900, minHeight: 600)
        }
        .windowStyle(.hiddenTitleBar)

        MenuBarExtra {
            ObservatoryMenuBarView()
                .environment(engine)
        } label: {
            Image(systemName: engine.streamConnected ? "dot.radiowaves.left.and.right" : "waveform.path.ecg")
        }
        .menuBarExtraStyle(.menu)
    }
}
