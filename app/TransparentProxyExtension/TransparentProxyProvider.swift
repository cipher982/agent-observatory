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
                self.relayViaGoProxy(flow, firstBytes: buf, host: sni)
            default:
                // No SNI, not allowlisted, or peek budget exhausted: pass through
                // by relaying DIRECT to the real destination (we already took it).
                self.relayDirect(flow, firstBytes: buf, remote: remote)
            }
        }
    }

    // Allowlisted: tunnel through the Go MITM proxy via HTTP CONNECT.
    private func relayViaGoProxy(_ flow: NEAppProxyTCPFlow, firstBytes: Data, host: String) {
        let conn = NWConnection(
            host: NWEndpoint.Host(goProxyHost),
            port: NWEndpoint.Port(rawValue: goProxyPort)!,
            using: .tcp
        )
        conn.start(queue: .global())
        let connectReq = "CONNECT \(host):443 HTTP/1.1\r\nHost: \(host):443\r\n\r\n"
        conn.send(content: Data(connectReq.utf8), completion: .contentProcessed { [weak self] _ in
            self?.readConnectResponse(conn) { ok in
                guard let self else { return }
                guard ok else { log.error("go proxy CONNECT rejected for \(host, privacy: .public)"); self.close(flow, conn); return }
                conn.send(content: firstBytes, completion: .contentProcessed { _ in
                    self.pump(flow: flow, conn: conn)
                })
            }
        })
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
    private func pump(flow: NEAppProxyTCPFlow, conn: NWConnection) {
        func flowToConn() {
            flow.readData { data, err in
                guard let data, !data.isEmpty, err == nil else {
                    conn.send(content: nil, isComplete: true, completion: .idempotent)
                    return
                }
                conn.send(content: data, completion: .contentProcessed { e in
                    if e == nil { flowToConn() }
                })
            }
        }
        func connToFlow() {
            conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, isComplete, err in
                if let data, !data.isEmpty {
                    flow.write(data) { werr in
                        if werr == nil && !isComplete { connToFlow() }
                    }
                } else if isComplete || err != nil {
                    flow.closeWriteWithError(err)
                    conn.cancel()
                }
            }
        }
        flowToConn()
        connToFlow()
    }

    private func close(_ flow: NEAppProxyTCPFlow, _ conn: NWConnection?) {
        flow.closeReadWithError(nil)
        flow.closeWriteWithError(nil)
        conn?.cancel()
    }
}
