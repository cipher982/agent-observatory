import XCTest
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

    func testAllowlistMatching() {
        let a = Allowlist.providers
        XCTAssertTrue(a.contains("api.anthropic.com"))
        XCTAssertTrue(a.contains("api.openai.com"))
        XCTAssertTrue(a.contains("bedrock-runtime.us-east-1.amazonaws.com"))
        XCTAssertTrue(a.contains("API.ANTHROPIC.COM"))            // case-insensitive
        XCTAssertTrue(a.contains("api.anthropic.com."))           // trailing dot
        XCTAssertFalse(a.contains("example.com"))
        XCTAssertFalse(a.contains("evil-amazonaws.com"))          // lookalike, not a subdomain
        XCTAssertFalse(a.contains("notanthropic.com"))
    }
}
