import Foundation

enum CapturePauseGate {
    static let path = "/tmp/agent-observatory-capture-paused"

    static func isPaused() -> Bool {
        FileManager.default.fileExists(atPath: path)
    }
}
