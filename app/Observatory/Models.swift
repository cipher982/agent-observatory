import Foundation

// Codable types mirroring the Go engine's fact-level JSON (v2):
// `agents serve` /api/sessions -> []SessionView with per-fact evidence.

struct SessionView: Codable, Identifiable {
    let session: Session
    let workspace: String
    let summaryLevel: String        // "none" | "observed" | "verified"
    let facts: [FactResult]
    let activeSkills: [String]?
    let activeTools: [String]?
    let sourceStatus: [SourceStatus]?

    var id: String { session.SessionID.isEmpty ? session.Path : session.SessionID }
}

struct Session: Codable {
    let Runtime: String
    let SessionID: String
    let Path: String
    let CWD: String
    let GitRepo: String
    let GitBranch: String
    let StartedAt: String
    let LastActivity: String
    let Version: String
    let RecordCount: Int
}

struct SourceStatus: Codable, Identifiable {
    let source: String
    let available: Bool
    let reason: String?
    var id: String { source }
}

// fact.FactResult — keys are lowercase per the Go json tags.
struct FactResult: Codable, Identifiable {
    let key: FactKey
    let expectation: Expectation?
    let observations: [FactObservation]?
    let status: String          // expected_observed | expected_verified | missing_expected | conflict | coverage_gap | unexpected | unknown
    let best_level: Int?        // 0 observed, 1 verified

    var id: String { key.kind + ":" + key.runtime + ":" + key.name }
}

struct FactKey: Codable {
    let kind: String            // instruction_text | tool_available
    let runtime: String
    let name: String
    let digest: String?
}

struct Expectation: Codable {
    let required: Bool
    let origin: String
}

struct FactObservation: Codable, Identifiable {
    let source: String          // transcript | wire
    let polarity: Int           // 0 present, 1 absent
    let level: Int              // 0 observed, 1 verified
    let coverage: Int           // 0 complete, 1 positive_only, 2 heuristic, 3 none
    let match: String
    let detail: String?
    var id: String { source + ":" + String(level) + ":" + String(polarity) }
}

// MARK: - View helpers

enum FactStatus: String {
    case expectedObserved = "expected_observed"
    case expectedVerified = "expected_verified"
    case missingExpected  = "missing_expected"
    case conflict         = "conflict"
    case coverageGap      = "coverage_gap"
    case unexpected       = "unexpected"
    case unknown          = "unknown"

    init(_ raw: String) { self = FactStatus(rawValue: raw) ?? .unknown }

    /// SF Symbol for the witness mark.
    var glyph: String {
        switch self {
        case .expectedVerified: return "checkmark.seal.fill"
        case .expectedObserved: return "checkmark.circle.fill"
        case .missingExpected:  return "exclamationmark.triangle.fill"
        case .conflict:         return "bolt.trianglebadge.exclamationmark.fill"
        case .coverageGap, .unexpected, .unknown: return "minus.circle.fill"
        }
    }

    var label: String {
        switch self {
        case .expectedVerified: return "verified on wire"
        case .expectedObserved: return "observed in transcript"
        case .missingExpected:  return "missing (drift)"
        case .conflict:         return "CONFLICT"
        case .coverageGap:      return "unverifiable here"
        case .unexpected:       return "unexpected"
        case .unknown:          return "unknown"
        }
    }
}

extension FactResult {
    var statusEnum: FactStatus { FactStatus(status) }
    var displayName: String { key.kind == "tool_available" ? "tool: " + key.name : key.name }
}

extension SessionView {
    /// Counts by class for the row summary: (good, drift, conflict, gap).
    var markCounts: (good: Int, drift: Int, conflict: Int, gap: Int) {
        var good = 0, drift = 0, conflict = 0, gap = 0
        for f in facts {
            switch f.statusEnum {
            case .expectedObserved, .expectedVerified: good += 1
            case .missingExpected: drift += 1
            case .conflict: conflict += 1
            default: gap += 1
            }
        }
        return (good, drift, conflict, gap)
    }

    var shortCWD: String {
        let home = NSHomeDirectory()
        if session.CWD.hasPrefix(home) { return "~" + session.CWD.dropFirst(home.count) }
        return session.CWD.isEmpty ? "—" : session.CWD
    }

    var lastActivityDate: Date? { ISO8601DateFormatter().date(from: session.LastActivity) }
}
