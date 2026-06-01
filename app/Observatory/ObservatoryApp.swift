import SwiftUI

@main
struct ObservatoryApp: App {
    @State private var engine = EngineClient(apiPort: 7878, proxyPort: 7879)
    @State private var proxy = ProxyController()

    var body: some Scene {
        WindowGroup("Agent Observatory", id: "main") {
            ContentView()
                .environment(engine)
                .environment(proxy)
                .frame(minWidth: 900, minHeight: 600)
        }
        .windowStyle(.hiddenTitleBar)

        MenuBarExtra {
            ObservatoryMenuBarView()
                .environment(engine)
                .environment(proxy)
        } label: {
            MenuBarDomeIcon(degraded: !engine.streamConnected)
        }
        .menuBarExtraStyle(.menu)
    }
}
