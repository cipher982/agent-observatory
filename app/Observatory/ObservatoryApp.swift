import SwiftUI

@main
struct ObservatoryApp: App {
    @State private var engine = EngineClient(apiPort: 7878, proxyPort: 7879)

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(engine)
                .frame(minWidth: 900, minHeight: 600)
        }
        .windowStyle(.hiddenTitleBar)
    }
}
