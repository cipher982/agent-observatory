// Package fact is the typed evidence model for the Observatory (the v2 core,
// designed in the Jeff-Dean review). Its central idea: confidence is a property
// of a FACT, not a session, and the resolver's expectation is NOT evidence — it
// is the thing being tested.
//
// Pipeline:
//
//	resolver   → []Expectation   (what SHOULD be present; the claim under test)
//	evidence   → []Observation   (what a source actually saw; OBSERVED or VERIFIED)
//	Merge      → []FactResult     (per-fact bundle: expectation + all observations
//	                               + computed status, keeping ALL observations)
//
// Hard rules enforced here (do not soften):
//   - A green/verified status may never rest on an Expectation alone.
//   - A source may only assert ABSENCE when its Coverage is Complete.
//   - CONFLICT requires same FactKey + same epoch + both Complete coverage.
//     Anything weaker is Incomparable / CoverageGap, never a conflict.
package fact

// Level is the evidence strength. EXPECTED is intentionally NOT here — an
// expectation is not evidence. Only these two levels are evidence.
type Level int

const (
	Observed Level = iota // the CLI's own transcript recorded it
	Verified              // captured on the wire (strongest)
)

func (l Level) String() string {
	switch l {
	case Observed:
		return "observed"
	case Verified:
		return "verified"
	default:
		return "unknown"
	}
}

// Kind is the type of fact. Typed, not stringly — prevents bogus cross-kind merges.
type Kind string

const (
	InstructionText Kind = "instruction_text" // AGENTS.md / doctrine present in prompt
	ToolAvailable   Kind = "tool_available"   // an MCP/tool was offered to the agent
)

// FactKey is the canonical identity of a fact. Observations and expectations are
// only ever compared when their keys are equal. Runtime/Provider keep otherwise-
// identical names from different runtimes distinct.
type FactKey struct {
	Kind    Kind   `json:"kind"`
	Runtime string `json:"runtime"` // claude | codex | antigravity
	Name    string `json:"name"`    // canonical, kind-specific (e.g. tool server name)
	Digest  string `json:"digest,omitempty"` // optional content digest for text facts
}

// Polarity is whether a fact was present or absent.
type Polarity int

const (
	Present Polarity = iota
	Absent
)

// Coverage describes what a source is CAPABLE of proving for a fact — the rule
// that prevents false drift/conflict. A PositiveOnly source (Codex tools =
// invoked-only) can prove Present but never Absent.
type Coverage int

const (
	CoverageComplete     Coverage = iota // can prove present AND absent
	CoveragePositiveOnly                 // can only prove present; absence proves nothing
	CoverageHeuristic                    // fuzzy match (e.g. marker substring) — never VERIFIED-eligible
	CoverageNone                         // source cannot speak to this fact at all
)

func (c Coverage) String() string {
	switch c {
	case CoverageComplete:
		return "complete"
	case CoveragePositiveOnly:
		return "positive_only"
	case CoverageHeuristic:
		return "heuristic"
	default:
		return "none"
	}
}

// MatchMethod records HOW a text fact was matched. Only digest matches are
// eligible to back a VERIFIED-green; marker substring is heuristic.
type MatchMethod string

const (
	MatchExactDigest      MatchMethod = "exact_digest"
	MatchNormalizedDigest MatchMethod = "normalized_digest"
	MatchMarkerHeuristic  MatchMethod = "marker_heuristic"
	MatchToolName         MatchMethod = "tool_name"
	MatchNone             MatchMethod = ""
)

// Epoch identifies the assembly moment a fact belongs to, so observations from
// different sources are only conflated when they describe the SAME request/turn.
// Timestamps alone are insufficient (transcript and wire observe at different
// moments) — correlation is by SessionID plus, when available, RequestID.
type Epoch struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id,omitempty"`
}

// Comparable reports whether two epochs may be compared for conflict detection.
func (e Epoch) Comparable(o Epoch) bool {
	if e.SessionID == "" || o.SessionID == "" || e.SessionID != o.SessionID {
		return false
	}
	// If both carry a RequestID they must match; if either lacks one we fall back
	// to session-level comparability (best effort — still gated by Coverage below).
	if e.RequestID != "" && o.RequestID != "" {
		return e.RequestID == o.RequestID
	}
	return true
}

// Expectation is what the resolver believes SHOULD hold. NOT evidence.
type Expectation struct {
	Key      FactKey `json:"key"`
	Required bool    `json:"required"`
	Origin   string  `json:"origin"` // resolver layer that produced it (provenance)
	Detail   string  `json:"detail,omitempty"`
}

// Observation is one source's claim about one fact. This IS evidence.
type Observation struct {
	Key      FactKey     `json:"key"`
	Polarity Polarity    `json:"polarity"`
	Level    Level       `json:"level"`
	Source   string      `json:"source"` // "transcript" | "wire"
	Coverage Coverage    `json:"coverage"`
	Match    MatchMethod `json:"match"`
	Epoch    Epoch       `json:"epoch"`
	Detail   string      `json:"detail,omitempty"`
}

// Status is the computed verdict for a fact after merging expectation + observations.
type Status string

const (
	StatusExpectedObserved Status = "expected_observed" // expected, transcript-observed present
	StatusExpectedVerified Status = "expected_verified" // expected, wire-verified present
	StatusMissingExpected  Status = "missing_expected"  // expected, a Complete source proved absent
	StatusUnexpected       Status = "unexpected"        // observed present but not expected
	StatusConflict         Status = "conflict"          // two Complete sources disagree, same epoch
	StatusCoverageGap      Status = "coverage_gap"      // expected, but no source can prove presence/absence
	StatusUnknown          Status = "unknown"
)

// FactResult is the merged bundle for one fact. ALL observations are retained —
// the merge never discards a disagreeing observation (that would hide conflicts).
type FactResult struct {
	Key          FactKey       `json:"key"`
	Expectation  *Expectation  `json:"expectation,omitempty"`
	Observations []Observation `json:"observations"`
	Status       Status        `json:"status"`
	BestLevel    *Level        `json:"best_level,omitempty"`
}
