import Foundation
import Observation

// EngineClient owns the Go engine in MONITOR mode: it runs the JSON API, a live
// SSE stream of in-flight LLM requests, and an always-on intercepting proxy.
// The app is a pure renderer — it polls /api/sessions and consumes /api/stream
// for realtime activity.

// A single live, in-flight LLM request captured on the wire.
struct LiveEvent: Identifiable, Codable {
    let id = UUID()
    let host: String
    let endpoint: String
    let runtime: String
    let systemChars: Int
    let agentsMarker: Bool
    let markerSlot: String
    let toolCount: Int
    let toolNames: [String]?
    let at: String

    private enum CodingKeys: String, CodingKey {
        case host, endpoint, runtime, systemChars, agentsMarker, markerSlot, toolCount, toolNames, at
    }
}

@MainActor
@Observable
final class EngineClient {
    enum State: Equatable { case starting, running, failed(String) }

    private(set) var state: State = .starting
    private(set) var sessions: [SessionView] = []
    private(set) var liveEvents: [LiveEvent] = []      // newest first
    private(set) var lastUpdated: Date?
    private(set) var proxyCommand: String = ""          // shown in the GUI
    private(set) var streamConnected = false
    private(set) var pulse = 0                           // increments on each live event (drives animations)

    private let apiPort: Int
    private let proxyPort: Int
    private var process: Process?
    private var pollTask: Task<Void, Never>?
    private var streamTask: Task<Void, Never>?
    private let session = URLSession(configuration: .ephemeral)

    init(apiPort: Int = 7878, proxyPort: Int = 7879) {
        self.apiPort = apiPort
        self.proxyPort = proxyPort
    }

    var baseURL: URL { URL(string: "http://127.0.0.1:\(apiPort)")! }

    func start() {
        startEngineIfNeeded()
        pollTask?.cancel()
        pollTask = Task { [weak self] in await self?.pollLoop() }
        streamTask?.cancel()
        streamTask = Task { [weak self] in await self?.streamLoop() }
    }

    func stop() {
        pollTask?.cancel(); streamTask?.cancel()
        process?.terminate(); process = nil
    }

    private func startEngineIfNeeded() {
        guard let helper = Self.bundledHelperURL() else { return }
        let p = Process()
        p.executableURL = helper
        var monitorArgs = ["monitor", "--port", "\(apiPort)", "--proxy-port", "\(proxyPort)"]
        if ProcessInfo.processInfo.environment["OBSERVATORY_DEMO"] == "1" { monitorArgs.append("--demo") }
        p.arguments = monitorArgs
        p.standardOutput = Pipe(); p.standardError = Pipe()
        do { try p.run(); process = p }
        catch { state = .failed("could not launch engine: \(error.localizedDescription)") }
    }

    static func bundledHelperURL() -> URL? {
        if let url = Bundle.main.url(forResource: "agents", withExtension: nil) { return url }
        let candidates = [
            "\(NSHomeDirectory())/git/agent-observatory/backend/agents",
            "/tmp/obsm",
        ]
        for c in candidates where FileManager.default.isExecutableFile(atPath: c) {
            return URL(fileURLWithPath: c)
        }
        return nil
    }

    // MARK: polling /api/sessions + /api/proxy

    private func pollLoop() async {
        for attempt in 0..<12 {
            if Task.isCancelled { return }
            if await healthOK() { break }
            try? await Task.sleep(for: .milliseconds(attempt < 3 ? 200 : 500))
        }
        await loadProxyCoords()
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: .seconds(4))
        }
    }

    private func healthOK() async -> Bool {
        var req = URLRequest(url: baseURL.appendingPathComponent("healthz"))
        req.timeoutInterval = 2
        guard let (_, resp) = try? await session.data(for: req),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return false }
        return true
    }

    private func loadProxyCoords() async {
        let url = baseURL.appendingPathComponent("api/proxy")
        guard let (data, _) = try? await session.data(from: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: String],
              let proxy = obj["httpsProxy"], let ca = obj["caPath"] else { return }
        proxyCommand = "HTTPS_PROXY=\(proxy) NODE_EXTRA_CA_CERTS=\(ca) SSL_CERT_FILE=\(ca) AWS_CA_BUNDLE=\(ca) claude -p \"hello\""
    }

    func refresh() async {
        let url = baseURL.appendingPathComponent("api/sessions")
        do {
            let (data, resp) = try await session.data(from: url)
            guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else {
                state = .failed("engine returned a non-200 response"); return
            }
            sessions = try JSONDecoder().decode([SessionView].self, from: data)
            lastUpdated = Date()
            state = .running
        } catch {
            if sessions.isEmpty { state = .failed(error.localizedDescription) }
        }
    }

    // MARK: live SSE stream

    private func streamLoop() async {
        while !Task.isCancelled {
            await consumeStream()
            streamConnected = false
            try? await Task.sleep(for: .seconds(2)) // reconnect backoff
        }
    }

    private func consumeStream() async {
        let url = baseURL.appendingPathComponent("api/stream")
        var req = URLRequest(url: url)
        req.timeoutInterval = .infinity
        req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        guard let (bytes, resp) = try? await session.bytes(for: req),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return }
        streamConnected = true
        var eventName = ""
        do {
            for try await line in bytes.lines {
                if Task.isCancelled { return }
                if line.hasPrefix("event:") {
                    eventName = line.dropFirst(6).trimmingCharacters(in: .whitespaces)
                } else if line.hasPrefix("data:") {
                    let json = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                    if eventName == "capture", let data = json.data(using: .utf8),
                       let ev = try? JSONDecoder().decode(LiveEvent.self, from: data) {
                        liveEvents.insert(ev, at: 0)
                        if liveEvents.count > 50 { liveEvents.removeLast() }
                        pulse += 1
                    }
                }
            }
        } catch { /* stream dropped; loop reconnects */ }
    }
}
