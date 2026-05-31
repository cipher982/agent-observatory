import Foundation

// Allowlist decides whether an outbound flow's SNI hostname belongs to an LLM
// provider we want to capture. Everything not on the list is passed through the
// transparent proxy untouched. Matching is exact-or-subdomain on a small set of
// suffixes, so e.g. "bedrock-runtime.us-east-1.amazonaws.com" matches the
// "amazonaws.com" entry while "example.com" does not.
//
// This mirrors the Go proxy's defaultInspectHost allowlist (backend/internal/
// wire/proxy.go); keep the two in sync. The Go side is the authority for which
// CONNECT targets actually get TLS-terminated; this is the kernel-flow gate.
struct Allowlist {
    let suffixes: [String]

    static let providers = Allowlist(suffixes: [
        "api.openai.com",
        "api.anthropic.com",
        "amazonaws.com",            // bedrock-runtime.<region>.amazonaws.com
        "aws-external-anthropic",   // aws-external-anthropic.<region>...
    ])

    // Matches when host equals a suffix or is a dotted subdomain of it.
    func contains(_ host: String) -> Bool {
        let h = host.lowercased().trimmingTrailingDot()
        for s in suffixes {
            if h == s || h.hasSuffix("." + s) { return true }
            // bare-token entries (e.g. "aws-external-anthropic") match as a label prefix
            if !s.contains(".") && (h == s || h.hasPrefix(s + ".") || h.contains("." + s + ".")) {
                return true
            }
        }
        return false
    }
}

private extension String {
    func trimmingTrailingDot() -> String {
        hasSuffix(".") ? String(dropLast()) : self
    }
}
