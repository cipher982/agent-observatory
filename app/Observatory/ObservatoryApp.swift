import SwiftUI

@main
struct ObservatoryApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var engine = EngineClient(apiPort: 7878, proxyPort: 7879)
    @State private var proxy = ProxyController()

    init() {
        CaptureResetCommand.runIfRequested()
    }

    var body: some Scene {
        WindowGroup("Agent Observatory", id: "main") {
            ContentView()
                .environment(engine)
                .environment(proxy)
                .frame(minWidth: 900, minHeight: 600)
                .onAppear {
                    appDelegate.configure(engine: engine, proxy: proxy)
                }
        }
        .windowStyle(.hiddenTitleBar)
    }
}
