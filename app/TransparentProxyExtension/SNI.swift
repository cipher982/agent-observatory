import Foundation

// SNI extracts the Server Name Indication hostname from a TLS ClientHello.
//
// The transparent proxy's kernel rules match on IP/port only, so the per-host
// allowlist decision happens here: we peek the first bytes of an outbound :443
// flow, parse the ClientHello, and read the SNI. If we can't read an SNI (not
// TLS, no SNI extension, fragmented record, or a future Encrypted ClientHello),
// we return a result that tells the caller to FAIL OPEN — pass the flow through
// untouched rather than risk mis-handling it.
enum SNI {

    enum Result: Equatable {
        case host(String)   // SNI hostname successfully parsed
        case none           // definitively no SNI (valid TLS, no SNI extension, or not TLS)
        case needMore       // buffer too short; caller should read more bytes before deciding
    }

    // Parse a TLS ClientHello from the leading bytes of a TCP stream.
    //
    // TLS record layout we walk:
    //   TLSPlaintext: type(1)=22 handshake | version(2) | length(2) | fragment
    //   Handshake:    msg_type(1)=1 client_hello | length(3) | body
    //   ClientHello:  client_version(2) | random(32) | session_id<1> |
    //                 cipher_suites<2> | compression_methods<1> | extensions<2>
    //   Extension:    type(2) | data<2>; SNI type=0, ServerNameList<2>,
    //                 entry: name_type(1)=0 host_name | host_name<2>
    static func parse(_ data: Data) -> Result {
        let b = [UInt8](data)
        var i = 0

        func need(_ n: Int) -> Bool { i + n <= b.count }

        // --- TLS record header ---
        // Reject non-TLS as soon as the content-type byte is available, before
        // requiring the full 5-byte header (a short "GET ..." buffer is decisively
        // not a handshake record and must return .none, not .needMore).
        if b.isEmpty { return .needMore }
        guard b[0] == 0x16 else { return .none } // not a handshake record => not TLS we parse
        guard need(5) else { return .needMore }
        // b[1],b[2] = record version (ignore). b[3],b[4] = record length.
        i = 5

        // --- Handshake header ---
        guard need(4) else { return .needMore }
        guard b[i] == 0x01 else { return .none } // not a ClientHello
        let hsLen = Int(b[i + 1]) << 16 | Int(b[i + 2]) << 8 | Int(b[i + 3])
        i += 4
        let hsEnd = i + hsLen
        // We don't require the whole record to be present yet, but if the handshake
        // claims more than we have AND we haven't reached extensions, we may needMore.

        // --- ClientHello body ---
        guard need(2 + 32) else { return .needMore }
        i += 2 + 32 // client_version + random

        // session_id
        guard need(1) else { return .needMore }
        let sidLen = Int(b[i]); i += 1
        guard need(sidLen) else { return .needMore }
        i += sidLen

        // cipher_suites
        guard need(2) else { return .needMore }
        let csLen = Int(b[i]) << 8 | Int(b[i + 1]); i += 2
        guard need(csLen) else { return .needMore }
        i += csLen

        // compression_methods
        guard need(1) else { return .needMore }
        let cmLen = Int(b[i]); i += 1
        guard need(cmLen) else { return .needMore }
        i += cmLen

        // extensions
        guard need(2) else { return .needMore }
        let extTotal = Int(b[i]) << 8 | Int(b[i + 1]); i += 2
        let extEnd = min(i + extTotal, hsEnd)

        while i + 4 <= extEnd {
            let extType = Int(b[i]) << 8 | Int(b[i + 1])
            let extLen = Int(b[i + 2]) << 8 | Int(b[i + 3])
            i += 4
            guard i + extLen <= b.count else { return .needMore }
            let extDataStart = i

            if extType == 0x0000 { // server_name
                var j = extDataStart
                guard j + 2 <= b.count else { return .needMore }
                // ServerNameList length
                j += 2
                guard j + 3 <= b.count else { return .needMore }
                let nameType = b[j]; j += 1
                let nameLen = Int(b[j]) << 8 | Int(b[j + 1]); j += 2
                guard nameType == 0x00 else { return .none } // not host_name
                guard j + nameLen <= b.count else { return .needMore }
                let host = String(decoding: b[j ..< j + nameLen], as: UTF8.self)
                return host.isEmpty ? .none : .host(host.lowercased())
            }
            i = extDataStart + extLen
        }

        // Parsed a full ClientHello with no SNI extension.
        return .none
    }
}
