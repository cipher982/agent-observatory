import XCTest
import SystemExtensions
// SNI.swift and Allowlist.swift are compiled directly into this test bundle
// (see project.yml) — they're pure logic with no NetworkExtension dependency.

// Unit tests for the transparent proxy's host-allowlist gate: the SNI parser and
// the suffix matcher. Fixtures in SNIFixtures_generated.swift are real
// ClientHello byte streams emitted by Go's crypto/tls for the named hostnames.
final class SNITests: XCTestCase {

    func testParsesAnthropicSNI() {
        XCTAssertEqual(SNI.parse(Data(Fixtures.api_anthropic_com)), .host("api.anthropic.com"))
    }

    func testParsesExampleSNI() {
        XCTAssertEqual(SNI.parse(Data(Fixtures.example_com)), .host("example.com"))
    }

    func testTruncatedBufferNeedsMore() {
        XCTAssertEqual(SNI.parse(Data(Fixtures.api_anthropic_com.prefix(10))), .needMore)
    }

    func testEmptyBufferNeedsMore() {
        XCTAssertEqual(SNI.parse(Data()), .needMore)
    }

    func testNonTLSReturnsNone() {
        // "GET " — a short, decisively non-handshake buffer must NOT stall as needMore.
        XCTAssertEqual(SNI.parse(Data([0x47, 0x45, 0x54, 0x20])), .none)
    }

    // Fragmentation fuzz: the parser runs on every partial read, so NO prefix of a
    // real ClientHello may crash (out-of-bounds) — each must return needMore or a
    // valid result. Guards the P0 fix.
    func testNoCrashOnAnyPrefix() {
        let full = Fixtures.api_anthropic_com
        for n in 0...full.count {
            _ = SNI.parse(Data(full.prefix(n)))  // a crash here fails the test
        }
        XCTAssertEqual(SNI.parse(Data(full.prefix(120))), .needMore)
    }

    // A ClientHello that claims a 65535-byte extensions block but supplies none
    // must not index past the buffer.
    func testLyingExtensionLengthDoesNotCrash() {
        var evil: [UInt8] = [0x16, 0x03, 0x01, 0x00, 0x40, 0x01, 0x00, 0x00, 0x3c, 0x03, 0x03]
        evil += [UInt8](repeating: 0, count: 32) // random
        evil += [0x00]                            // session id len 0
        evil += [0x00, 0x02, 0x13, 0x01]          // cipher suites
        evil += [0x01, 0x00]                      // compression
        evil += [0xff, 0xff]                      // extensions length = 65535 (lie)
        let r = SNI.parse(Data(evil))
        XCTAssertTrue(r == .needMore || r == .none)
    }

    func testAllowlistMatching() {
        let a = Allowlist.providers
        // Allowed — must mirror Go defaultInspectHost exactly.
        XCTAssertTrue(a.contains("api.anthropic.com"))
        XCTAssertTrue(a.contains("api.openai.com"))
        XCTAssertTrue(a.contains("generativelanguage.googleapis.com"))
        XCTAssertTrue(a.contains("bedrock-runtime.us-east-1.amazonaws.com"))
        XCTAssertTrue(a.contains("aws-external-anthropic.us-east-1.api.aws"))
        XCTAssertTrue(a.contains("API.ANTHROPIC.COM"))            // case-insensitive
        XCTAssertTrue(a.contains("api.anthropic.com."))           // trailing dot
        // Denied — these must NOT be diverted.
        XCTAssertFalse(a.contains("example.com"))
        XCTAssertFalse(a.contains("evil-amazonaws.com"))          // lookalike
        XCTAssertFalse(a.contains("amazonaws.com.attacker.com"))  // suffix-spoof
        XCTAssertFalse(a.contains("s3.amazonaws.com"))            // AWS but NOT bedrock
        XCTAssertFalse(a.contains("aws-external-anthropic.evil.com")) // right prefix, wrong suffix
        XCTAssertFalse(a.contains("notanthropic.com"))
        XCTAssertFalse(a.contains("api.openai.com.evil.com"))     // not exact
        XCTAssertFalse(a.contains("generativelanguage.googleapis.com.evil.com"))
    }

    func testCapturePauseGatePathContract() {
        XCTAssertEqual(CapturePauseGate.path, "/tmp/agent-observatory-capture-paused")
    }

    func testSourceMetadataHeadersAreSanitized() {
        let meta = SourceMetadata(signingIdentifier: "com.example.tool\r\nX-Bad: nope", pid: 42)
        let headers = Dictionary(uniqueKeysWithValues: meta.connectHeaders)
        XCTAssertEqual(headers["X-Agent-Observatory-Transparent-Flow"], "1")
        XCTAssertEqual(headers["X-Agent-Observatory-Source-Signing-ID"], "com.example.toolX-Bad: nope")
        XCTAssertEqual(headers["X-Agent-Observatory-Source-PID"], "42")
    }

    func testActivationErrorExplainsGatekeeperSignatureFailure() {
        let error = NSError(
            domain: OSSystemExtensionErrorDomain,
            code: 8,
            userInfo: [NSLocalizedDescriptionKey: "code signature invalid"]
        )
        let message = ActivationErrorFormatter.message(for: error)
        XCTAssertTrue(message.localizedCaseInsensitiveContains("notarized"), message)
        XCTAssertTrue(message.localizedCaseInsensitiveContains("Gatekeeper"), message)
    }
}
