import Foundation
import NetworkExtension
import Network
import os

private let log = Logger(subsystem: "com.github.cipher982.agentobservatory.ext", category: "proxy")

// TransparentProxyProvider captures outbound LLM-provider TLS flows and relays
// them through the local Go MITM proxy (127.0.0.1:goProxyPort) via HTTP CONNECT,
// so the existing Go body-parsing/CA logic is reused unchanged. Non-provider
// flows are passed through untouched.
//
// The provider never decrypts TLS itself — it sees ciphertext. The hostname
// decision is made from the ClientHello SNI peeked off the first bytes of each
// flow. See ne-epic-spec.md.
final class TransparentProxyProvider: NETransparentProxyProvider {

    private let goProxyHost = "127.0.0.1"
    private let goProxyPort: UInt16 = 7879
    private let allow = Allowlist.providers

    override func startProxy(options: [String: Any]? = nil,
                             completionHandler: @escaping @Sendable ((any Error)?) -> Void) {
        setTunnelNetworkSettings(ProxySettings.make()) { error in
            if let error { log.error("startProxy settings failed: \(error.localizedDescription)") }
            completionHandler(error)
        }
    }

    override func stopProxy(with reason: NEProviderStopReason,
                            completionHandler: @escaping @Sendable () -> Void) {
        log.log("stopProxy reason=\(reason.rawValue)")
        completionHandler()
    }

    // Signing identifiers we must NOT intercept, or we'd create a routing loop:
    // the Go proxy terminates TLS then dials the REAL provider:443 to forward —
    // and that dial is itself a :443 flow. Without this bypass the extension
    // re-intercepts the proxy's own upstream and loops it back, so the forward
    // never reaches the provider (agent gets 502). Excluding our daemon's source
    // app is the NE equivalent of mitmproxy's "exclude the proxy's own user".
    private let bypassSourceIdentifiers: Set<String> = [
        "com.github.cipher982.agentobservatory.agents",                          // the Go daemon/helper
        "com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension", // self
    ]

    // Dev-only safe-iteration scope. If the marker file exists, intercept ONLY
    // flows whose source app signing id is listed in it; pass EVERYTHING else
    // through. This can only NARROW interception, never widen it, so a developer
    // can exercise the real kernel path against a throwaway harness while their
    // own agents (Codex/Claude/browser) stay untouched. Mirrors mitmproxy's
    // macOS local mode (scope capture to specific apps). Absent in normal use.
    private static let devScopePath = "/tmp/agent-observatory-dev-scope"
    private lazy var devScopeAllowlist: Set<String>? = Self.loadDevScope()

    private static func loadDevScope() -> Set<String>? {
        guard let text = try? String(contentsOfFile: devScopePath, encoding: .utf8) else { return nil }
        let ids = text.split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty && !$0.hasPrefix("#") }
        return ids.isEmpty ? nil : Set(ids)
    }

    // Returning false hands the flow back to the kernel for direct, untouched
    // delivery (transparent-proxy semantics). We only take TCP :443 flows; once
    // we return true we are committed to the flow, so a non-allowlisted SNI
    // discovered after the peek is relayed DIRECT to its real destination.
    override func handleNewFlow(_ flow: NEAppProxyFlow) -> Bool {
        guard let tcp = flow as? NEAppProxyTCPFlow,
              let remote = tcp.remoteEndpoint as? NWHostEndpoint,
              remote.port == "443" else {
            return false
        }
        let source = flow.metaData.sourceAppSigningIdentifier
        // Never intercept our own daemon's upstream connections (avoids the loop).
        if bypassSourceIdentifiers.contains(source) {
            log.log("bypass own flow source=\(source, privacy: .public) -> \(remote.hostname, privacy: .public)")
            return false
        }
        // Dev scope: when active, intercept ONLY the allowlisted test app(s).
        if let scope = devScopeAllowlist, !scope.contains(source) {
            log.log("dev-scope pass-through source=\(source, privacy: .public) -> \(remote.hostname, privacy: .public)")
            return false
        }
        open(tcp, remote: remote)
        return true
    }

    private func open(_ flow: NEAppProxyTCPFlow, remote: NWHostEndpoint) {
        flow.open(withLocalEndpoint: nil) { [weak self] err in
            guard let self else { return }
            if let err {
                log.error("flow.open failed: \(err.localizedDescription)")
                self.close(flow, nil)
                return
            }
            self.peekAndRoute(flow, remote: remote, accumulated: Data())
        }
    }

    // Read until we can decide SNI, then route. Bounds the peek so a slow/odd
    // client can't make us buffer unboundedly.
    private func peekAndRoute(_ flow: NEAppProxyTCPFlow, remote: NWHostEndpoint, accumulated: Data) {
        flow.readData { [weak self] data, err in
            guard let self else { return }
            guard let data, !data.isEmpty, err == nil else { self.close(flow, nil); return }
            let buf = accumulated + data

            switch SNI.parse(buf) {
            case .needMore where buf.count < 8 * 1024:
                self.peekAndRoute(flow, remote: remote, accumulated: buf)
            case .host(let sni) where self.allow.contains(sni):
                log.log("capture flow sni=\(sni, privacy: .public)")
                self.relayViaGoProxy(flow, firstBytes: buf, host: sni, remote: remote)
            default:
                // No SNI, not allowlisted, or peek budget exhausted: pass through
                // by relaying DIRECT to the real destination (we already took it).
                self.relayDirect(flow, firstBytes: buf, remote: remote)
            }
        }
    }

    // Allowlisted: tunnel through the Go MITM proxy via HTTP CONNECT. If the proxy
    // is down or rejects the CONNECT (e.g. the daemon isn't running), fail OPEN by
    // relaying direct to the real upstream — capture is best-effort, but we must
    // never break the agent's request just because Observatory isn't listening.
    private func relayViaGoProxy(_ flow: NEAppProxyTCPFlow, firstBytes: Data, host: String, remote: NWHostEndpoint) {
        let conn = NWConnection(
            host: NWEndpoint.Host(goProxyHost),
            port: NWEndpoint.Port(rawValue: goProxyPort)!,
            using: .tcp
        )
        conn.start(queue: .global())

        // The CONNECT handshake must complete promptly; if the proxy accepts TCP
        // but stalls, we fall back to a direct relay rather than hang the agent.
        // The handshake completion and the timeout fire on different queues, so a
        // lock makes "claim the outcome" atomic — we take exactly ONE path
        // (proxy, direct-fallback) and never send the agent's bytes twice.
        let outcome = Outcome()
        func failOpen(_ why: String) {
            guard outcome.claim() else { return }
            log.error("go proxy \(why, privacy: .public) for \(host, privacy: .public); relaying direct")
            conn.cancel()
            self.relayDirect(flow, firstBytes: firstBytes, remote: remote)
        }
        let timeout = DispatchWorkItem { failOpen("CONNECT timed out") }
        DispatchQueue.global().asyncAfter(deadline: .now() + 5, execute: timeout)

        let connectReq = "CONNECT \(host):443 HTTP/1.1\r\nHost: \(host):443\r\n\r\n"
        conn.send(content: Data(connectReq.utf8), completion: .contentProcessed { [weak self] sendErr in
            guard let self else { return }
            if sendErr != nil { failOpen("unreachable"); return }
            self.readConnectResponse(conn) { ok in
                guard ok else { failOpen("CONNECT rejected"); return }
                timeout.cancel()
                // Commit to the proxy. If we lost the race to the timeout, it has
                // already started a direct relay — bail.
                guard outcome.claim() else { return }
                conn.send(content: firstBytes, completion: .contentProcessed { e in
                    if e != nil {
                        // Proxy died after 200 but before our bytes reached an
                        // upstream, so no TLS handshake started — safe to retry direct.
                        log.error("go proxy dropped after CONNECT for \(host, privacy: .public); relaying direct")
                        conn.cancel()
                        self.relayDirect(flow, firstBytes: firstBytes, remote: remote)
                        return
                    }
                    self.pump(flow: flow, conn: conn)
                })
            }
        })
    }

    // Outcome is a one-shot, thread-safe latch: the first claim() wins and all
    // later claims return false. Used to pick a single relay path under races.
    private final class Outcome {
        private let lock = NSLock()
        private var claimed = false
        func claim() -> Bool {
            lock.lock(); defer { lock.unlock() }
            if claimed { return false }
            claimed = true
            return true
        }
    }

    // Not allowlisted (or SNI unreadable): relay straight to the real upstream so
    // the flow is delivered untouched.
    private func relayDirect(_ flow: NEAppProxyTCPFlow, firstBytes: Data, remote: NWHostEndpoint) {
        let conn = NWConnection(
            host: NWEndpoint.Host(remote.hostname),
            port: NWEndpoint.Port(rawValue: UInt16(remote.port) ?? 443)!,
            using: .tcp
        )
        conn.start(queue: .global())
        conn.send(content: firstBytes, completion: .contentProcessed { [weak self] _ in
            self?.pump(flow: flow, conn: conn)
        })
    }

    // Read the "HTTP/1.1 200 Connection Established\r\n\r\n" block from the Go
    // proxy, then signal success. Reads until the header terminator.
    private func readConnectResponse(_ conn: NWConnection, done: @escaping (Bool) -> Void) {
        var header = Data()
        func step() {
            conn.receive(minimumIncompleteLength: 1, maximumLength: 1024) { data, _, isComplete, err in
                if let data, !data.isEmpty { header.append(data) }
                if let terminator = header.range(of: Data("\r\n\r\n".utf8)) {
                    // status is "HTTP/1.1 2xx ..."; byte index 9 is the first status digit.
                    let ok = header.count > 9 && header[header.startIndex.advanced(by: 9)] == UInt8(ascii: "2")
                    _ = terminator
                    done(ok)
                    return
                }
                if isComplete || err != nil || header.count > 8 * 1024 { done(false); return }
                step()
            }
        }
        step()
    }

    // Bidirectional copy: flow.readData -> conn.send ; conn.receive -> flow.write.
    // On ANY error or EOF on either leg we tear down BOTH legs, so a half-open or
    // failed copy can never leak a flow/NWConnection or hang the client waiting for
    // an EOF that never comes.
    private func pump(flow: NEAppProxyTCPFlow, conn: NWConnection) {
        func teardown() { close(flow, conn) }

        // flow (agent) -> conn (upstream/proxy)
        func flowToConn() {
            flow.readData { data, err in
                if let err = err { self.log_err("flow read", err); teardown(); return }
                guard let data, !data.isEmpty else {
                    // EOF from the agent: signal end-of-stream upstream, keep the
                    // reverse direction alive until the upstream also finishes.
                    conn.send(content: nil, isComplete: true, completion: .idempotent)
                    return
                }
                conn.send(content: data, completion: .contentProcessed { e in
                    if e != nil { teardown(); return }
                    flowToConn()
                })
            }
        }

        // conn (upstream/proxy) -> flow (agent)
        func connToFlow() {
            conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, isComplete, err in
                if let err = err { self.log_err("conn recv", err); teardown(); return }
                if let data, !data.isEmpty {
                    flow.write(data) { werr in
                        if werr != nil { teardown(); return }
                        if isComplete {
                            // Final bytes delivered AND upstream closed: half-close
                            // the write side, then tear down. (The earlier bug wrote
                            // the data but never closed, hanging the client.)
                            flow.closeWriteWithError(nil)
                            teardown()
                            return
                        }
                        connToFlow()
                    }
                } else if isComplete {
                    flow.closeWriteWithError(nil)
                    teardown()
                }
            }
        }

        flowToConn()
        connToFlow()
    }

    private func log_err(_ where_: String, _ err: Error) {
        log.error("pump \(where_, privacy: .public): \(err.localizedDescription)")
    }

    // Idempotent teardown of both legs. NWConnection.cancel and the flow close
    // calls are safe to invoke more than once.
    private func close(_ flow: NEAppProxyTCPFlow, _ conn: NWConnection?) {
        flow.closeReadWithError(nil)
        flow.closeWriteWithError(nil)
        conn?.cancel()
    }
}
