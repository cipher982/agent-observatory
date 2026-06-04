import Foundation

enum CapturePauseGate {
    static let path = "/tmp/agent-observatory-capture-paused"

    static func clear() {
        try? FileManager.default.removeItem(atPath: path)
    }
}
