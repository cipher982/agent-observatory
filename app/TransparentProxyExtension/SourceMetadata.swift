import Darwin
import Foundation
import NetworkExtension

struct SourceMetadata {
    let signingIdentifier: String
    let pid: pid_t?

    static func from(_ metadata: NEFlowMetaData) -> SourceMetadata {
        SourceMetadata(
            signingIdentifier: metadata.sourceAppSigningIdentifier,
            pid: metadata.sourceAppAuditToken.flatMap(pidFromAuditToken)
        )
    }

    var connectHeaders: [(String, String)] {
        var headers: [(String, String)] = [
            ("X-Agent-Observatory-Transparent-Flow", "1")
        ]
        if let value = sanitized(signingIdentifier) {
            headers.append(("X-Agent-Observatory-Source-Signing-ID", value))
        }
        if let pid {
            headers.append(("X-Agent-Observatory-Source-PID", String(pid)))
        }
        return headers
    }
}

private func sanitized(_ value: String) -> String? {
    let trimmed = value
        .replacingOccurrences(of: "\r", with: "")
        .replacingOccurrences(of: "\n", with: "")
        .trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
}

private func pidFromAuditToken(_ data: Data) -> pid_t? {
    guard data.count >= MemoryLayout<audit_token_t>.size else { return nil }
    return data.withUnsafeBytes { raw in
        guard let base = raw.baseAddress else { return nil }
        let token = base.assumingMemoryBound(to: audit_token_t.self).pointee
        return audit_token_to_pid(token)
    }
}
