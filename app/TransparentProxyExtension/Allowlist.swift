import Foundation

// Allowlist decides whether an outbound flow's SNI hostname belongs to an LLM
// provider we want to capture. Everything not on the list is passed through the
// transparent proxy untouched.
//
// This is the kernel-flow gate; it MUST match the Go proxy's defaultInspectHost
// (backend/internal/wire/proxy.go) exactly, or we'd divert flows the Go side
// won't actually inspect. Mirror of that switch:
//   h == "api.openai.com" || h == "api.anthropic.com"
//   || (contains "bedrock-runtime." && hasSuffix ".amazonaws.com")
//   || hasPrefix "aws-external-anthropic."
struct Allowlist {
    static let providers = Allowlist()

    func contains(_ host: String) -> Bool {
        let h = host.lowercased().trimmingTrailingDot()
        if h == "api.openai.com" || h == "api.anthropic.com" { return true }
        if h.contains("bedrock-runtime.") && h.hasSuffix(".amazonaws.com") { return true }
        if h.hasPrefix("aws-external-anthropic.") { return true }
        return false
    }
}

private extension String {
    func trimmingTrailingDot() -> String {
        hasSuffix(".") ? String(dropLast()) : self
    }
}
