import Foundation
import Observation

// EngineClient owns the Go engine in MONITOR mode: it runs the JSON API, a live
// SSE stream of in-flight LLM requests, and an always-on intercepting proxy.
// The app is a pure renderer — it polls /api/sessions and consumes /api/stream
// for realtime activity.

// A single live, in-flight LLM request captured on the wire.
struct LiveEvent: Identifiable, Codable {
    let id = UUID()
    let type: String
    let host: String
    let endpoint: String
    let runtime: String
    let status: String?
    let reason: String?
    let source: String?
    let systemChars: Int
    let parsed: Bool
    let toolCount: Int
    let toolNames: [String]?
    let at: String

    private enum CodingKeys: String, CodingKey {
        case type, host, endpoint, runtime, status, reason, source, systemChars, parsed, toolCount, toolNames, at
    }

    var bypassed: Bool { type == "bypass" || status == "bypassed" }
}

enum InstallState: Equatable {
    case unknown
    case installed
    case partial
    case missing
    case unavailable(String)
}

private enum BundledHelper: Equatable {
    case ready(URL)
    case notFound
    case missingAt(String)
    case notExecutable(String)

    var url: URL? {
        if case .ready(let url) = self { return url }
        return nil
    }

    var message: String {
        switch self {
        case .ready:
            return ""
        case .notFound:
            return "Bundled agents helper was not found in this app bundle. Rebuild or reinstall Agent Observatory."
        case .missingAt(let path):
            return "Bundled agents helper is missing at \(path). Rebuild or reinstall Agent Observatory."
        case .notExecutable(let path):
            return "Bundled agents helper is not executable at \(path). Rebuild or reinstall Agent Observatory."
        }
    }
}

@MainActor
@Observable
final class EngineClient {
    enum State: Equatable { case starting, running, failed(String) }

    private(set) var state: State = .starting
    private(set) var mode: ObservatoryMode = .demo
    var demoMode: Bool { mode == .demo }
    private(set) var sessions: [SessionView] = []
    private(set) var liveEvents: [LiveEvent] = []      // newest first
    private(set) var lastUpdated: Date?
    private(set) var installState: InstallState = .unknown
    private(set) var installStatusText = "Install status not checked yet"
    private(set) var streamConnected = false
    private(set) var pulse = 0                           // increments on each live event (drives animations)
    // Non-nil when capture is paused or an agent rejected the capture CA.
    // Surfaced as a warning in live mode.
    private(set) var captureWarning: String?

    // Live capture is served by the installed launchd daemon on the fixed ports.
    // Demo mode runs an app-owned engine on DISTINCT ports so it never collides
    // with that daemon (an installed user opening the app in Demo mode must still
    // get demo data, not the daemon's live feed on the same port).
    private let daemonAPIPort: Int
    private let daemonProxyPort: Int
    private let demoAPIPort: Int
    private let demoProxyPort: Int
    private var process: Process?
    private var pollTask: Task<Void, Never>?
    private var streamTask: Task<Void, Never>?
    private let session = URLSession(configuration: .ephemeral)

    init(apiPort: Int = 7878, proxyPort: Int = 7879, demoAPIPort: Int = 7880, demoProxyPort: Int = 7881) {
        self.daemonAPIPort = apiPort
        self.daemonProxyPort = proxyPort
        self.demoAPIPort = demoAPIPort
        self.demoProxyPort = demoProxyPort
    }

    // The port the current mode talks to: demo => app-owned engine; live => the
    // installed daemon.
    private var apiPort: Int { mode == .demo ? demoAPIPort : daemonAPIPort }
    private var proxyPort: Int { mode == .demo ? demoProxyPort : daemonProxyPort }

    var baseURL: URL { URL(string: "http://127.0.0.1:\(apiPort)")! }
    var installReady: Bool {
        if case .installed = installState { return true }
        return false
    }
    var installCommand: String {
        let bundledHelper = Self.bundledHelper()
        guard let helper = bundledHelper.url else { return bundledHelper.message }
        let bin = Self.shellQuote(helper.path)
        return "\(bin) install && \(bin) status"
    }
    var installCommandPreview: String {
        let bundledHelper = Self.bundledHelper()
        guard let helper = bundledHelper.url else { return bundledHelper.message }
        let bin = Self.shellQuote(helper.path)
        return "\(bin) install\n\(bin) status"
    }
    var uninstallCommand: String {
        let bundledHelper = Self.bundledHelper()
        guard let helper = bundledHelper.url else { return bundledHelper.message }
        return "\(Self.shellQuote(helper.path)) uninstall"
    }
    var installCommandAvailable: Bool { Self.bundledHelper().url != nil }
    var helperLocationWarning: String? { Self.helperLocationWarning() }

    func start(mode: ObservatoryMode = .demo) {
        self.mode = mode
        startEngineIfNeeded(mode: mode)
        pollTask?.cancel()
        pollTask = Task { [weak self] in await self?.pollLoop() }
        streamTask?.cancel()
        streamTask = Task { [weak self] in await self?.streamLoop() }
        Task { [weak self] in await self?.checkInstallStatus() }
    }

    // Restarts are serialized through a single task chain: each one awaits the
    // previous restart (its stop AND start) before running, and a newer restart
    // supersedes an older queued one. This guarantees the old app-owned process
    // has fully exited — releasing its demo ports — before the next one spawns,
    // even under a fast Demo↔Live↔Demo toggle.
    private var restartChain: Task<Void, Never>?
    private var restartGeneration = 0

    func restart(mode newMode: ObservatoryMode) {
        restartGeneration += 1
        let generation = restartGeneration
        let previous = restartChain
        restartChain = Task { [weak self] in
            await previous?.value
            guard let self else { return }
            await self.stopAndWait()
            // A newer restart was requested while we waited — let it win.
            guard generation == self.restartGeneration else { return }
            self.mode = newMode
            self.state = .starting
            self.sessions = []
            self.liveEvents = []
            self.lastUpdated = nil
            self.streamConnected = false
            self.pulse = 0
            self.start(mode: newMode)
        }
    }

    func stop() {
        pollTask?.cancel(); streamTask?.cancel()
        process?.terminate(); process = nil
    }

    // Stop the poll/stream loops and WAIT for the app-owned process to fully exit
    // (waitUntilExit runs off the main actor). Only restart() calls this, and
    // restarts are serialized via restartChain, so there's no concurrent caller
    // that could observe process == nil while the old one is still exiting.
    private func stopAndWait() async {
        pollTask?.cancel(); streamTask?.cancel()
        guard let p = process else { return }
        process = nil
        await Task.detached {
            p.terminate()
            p.waitUntilExit()
        }.value
    }

    // The app is a pure renderer. LIVE capture is owned by the installed launchd
    // daemon — the app NEVER spawns a live engine (doing so would mint an
    // ephemeral CA agents don't trust, and fight the daemon for ports). It only
    // spawns its OWN engine for DEMO mode, on distinct demo ports.
    private func startEngineIfNeeded(mode: ObservatoryMode) {
        guard mode == .demo else { return } // live => render the daemon, never spawn
        guard process == nil else { return }
        let bundledHelper = Self.bundledHelper()
        guard let helper = bundledHelper.url else {
            state = .failed(bundledHelper.message)
            return
        }
        let p = Process()
        p.executableURL = helper
        p.arguments = ["monitor", "--port", "\(demoAPIPort)", "--proxy-port", "\(demoProxyPort)", "--demo"]
        // Drain stdout/stderr so a long demo session can't fill the pipe buffer
        // and block the child (the proxy logs per capture).
        let outPipe = Pipe(), errPipe = Pipe()
        outPipe.fileHandleForReading.readabilityHandler = { _ = $0.availableData }
        errPipe.fileHandleForReading.readabilityHandler = { _ = $0.availableData }
        p.standardOutput = outPipe; p.standardError = errPipe
        do { try p.run(); process = p }
        catch { state = .failed("could not launch demo engine: \(error.localizedDescription)") }
    }

    private static func bundledHelper() -> BundledHelper {
        guard let url = Bundle.main.url(forResource: "agents", withExtension: nil) else {
            return .notFound
        }
        let path = url.path
        var isDir: ObjCBool = false
        if !FileManager.default.fileExists(atPath: path, isDirectory: &isDir) || isDir.boolValue {
            return .missingAt(path)
        }
        if !FileManager.default.isExecutableFile(atPath: path) {
            return .notExecutable(path)
        }
        return .ready(url)
    }

    func checkInstallStatus() async {
        let bundledHelper = Self.bundledHelper()
        guard let helper = bundledHelper.url else {
            installState = .unavailable(bundledHelper.message)
            installStatusText = bundledHelper.message
            return
        }
        let result = await Self.runStatus(helper: helper)
        installState = Self.parseInstallState(result.output)
        switch installState {
        case .installed:
            installStatusText = "Live capture is installed."
        case .partial:
            installStatusText = "Install is partially present. Run uninstall or install again to repair it."
        case .missing:
            installStatusText = "Live capture is not installed yet."
        case .unknown:
            installStatusText = "Install status could not be determined."
        case .unavailable(let message):
            installStatusText = message
        }
    }

    nonisolated private static func runStatus(helper: URL) async -> (code: Int32, output: String) {
        await Task.detached {
            let p = Process()
            p.executableURL = helper
            p.arguments = ["status"]
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = pipe
            do {
                try p.run()
                p.waitUntilExit()
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                return (p.terminationStatus, String(data: data, encoding: .utf8) ?? "")
            } catch {
                return (-1, "could not run agents status at \(helper.path): \(error.localizedDescription)")
            }
        }.value
    }

    nonisolated private static func parseInstallState(_ output: String) -> InstallState {
        if output.contains("overall: installed") { return .installed }
        if output.contains("overall: partially installed") { return .partial }
        if output.contains("overall: not fully installed") { return .missing }
        if output.contains("could not run agents status") { return .unavailable(output) }
        return .unknown
    }

    nonisolated private static func shellQuote(_ raw: String) -> String {
        "'" + raw.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    nonisolated private static func helperLocationWarning() -> String? {
        let bundlePath = Bundle.main.bundlePath
        let homeApplications = NSHomeDirectory() + "/Applications/"
        if bundlePath.hasPrefix("/Applications/") || bundlePath.hasPrefix(homeApplications) {
            return nil
        }
        if bundlePath.hasPrefix("/Volumes/") {
            return "Move Agent Observatory to Applications before live-capture install. The daemon stores this helper path, and DMG paths disappear after ejecting."
        }
        if bundlePath.hasPrefix("/private/tmp/") || bundlePath.hasPrefix("/tmp/") || bundlePath.contains("/DerivedData/") {
            return "This app is running from a temporary build path. Live-capture install will point launchd at this helper path, so use an Applications build for new-user testing."
        }
        return "For live capture, run Agent Observatory from Applications so the installed helper path remains stable."
    }

    // MARK: polling /api/sessions + /api/proxy

    private func pollLoop() async {
        for attempt in 0..<12 {
            if Task.isCancelled { return }
            if await healthOK() { break }
            try? await Task.sleep(for: .milliseconds(attempt < 3 ? 200 : 500))
        }
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: .seconds(4))
        }
    }

    private func healthOK() async -> Bool {
        var req = URLRequest(url: baseURL.appendingPathComponent("healthz"))
        req.timeoutInterval = 2
        guard let (data, resp) = try? await session.data(for: req),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return false }
        // Surface the daemon's "an agent rejected our CA" signal in live mode.
        if mode == .live,
           let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            if let paused = obj["capturePaused"] as? Bool, paused {
                captureWarning = "Live capture paused after an agent rejected the capture certificate. Provider traffic is passing through; restart agents, then disable and re-enable capture to resume."
            } else if let fails = obj["clientTLSFailures"] as? Int, fails > 0 {
                let host = (obj["lastTLSFailHost"] as? String) ?? "a provider"
                captureWarning = "An agent rejected the capture certificate for \(host) (\(fails)×). Restart that agent so it picks up the trusted CA, or disable capture."
            } else if let bypasses = obj["bypassCount"] as? Int, bypasses > 0,
                      let reason = obj["lastBypassReason"] as? String {
                let host = (obj["lastBypassHost"] as? String) ?? "provider traffic"
                captureWarning = "Some provider traffic is passing through without body capture: \(host) — \(reason)."
            } else {
                captureWarning = nil
            }
        } else if mode != .live {
            captureWarning = nil
        }
        return true
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
            // Only surface a failure once we've actually given up (the poll loop
            // retries). In live mode a missing daemon is the likely cause, so say
            // so instead of a raw socket error.
            if sessions.isEmpty {
                state = .failed(mode == .live
                    ? "Live capture engine isn't running. Install it from onboarding (or run `agents install`), then enable the system extension."
                    : error.localizedDescription)
            }
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
                    if (eventName == "capture" || eventName == "bypass"),
                       let data = json.data(using: .utf8),
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
