package fact

import "testing"

// TestConflictE2E proves the highest-value feature end-to-end through Merge:
// when the transcript (OBSERVED) says a doctrine fact is PRESENT but the wire
// (VERIFIED) proves it ABSENT for the SAME session — and both are Complete
// coverage — the merge reports CONFLICT (the silent-regression alarm).
//
// It ALSO proves the inverse guard: a Codex positive-only "absence" must NEVER
// escalate to CONFLICT.
func TestConflictE2E(t *testing.T) {
	doctrine := FactKey{Kind: InstructionText, Runtime: "claude", Name: "AGENTS.md global doctrine"}
	ep := Epoch{SessionID: "s1"}

	// Transcript recorded the doctrine present; wire proves it absent on the wire.
	// (The regression class: the CLI's own record disagrees with what left the box.)
	results := Merge(
		[]Expectation{{Key: doctrine, Required: true, Origin: "global"}},
		[]Observation{
			{Key: doctrine, Polarity: Present, Level: Observed, Source: "transcript", Coverage: CoverageComplete, Epoch: ep},
			{Key: doctrine, Polarity: Absent, Level: Verified, Source: "wire", Coverage: CoverageComplete, Epoch: ep},
		},
	)
	if statusFor(results, doctrine) != StatusConflict {
		t.Fatalf("expected CONFLICT for transcript-present vs wire-absent, got %q", statusFor(results, doctrine))
	}

	// Inverse guard: a positive-only transcript absence + wire present must NOT
	// be a conflict (positive-only can't anchor), and must resolve to verified.
	tool := FactKey{Kind: ToolAvailable, Runtime: "codex", Name: "slack_hub"}
	res2 := Merge(
		[]Expectation{{Key: tool, Required: true}},
		[]Observation{
			{Key: tool, Polarity: Present, Level: Verified, Source: "wire", Coverage: CoverageComplete, Epoch: ep},
			// no transcript observation at all (positive-only didn't invoke it) →
			// absence is simply not asserted, so no conflict is even possible.
		},
	)
	if got := statusFor(res2, tool); got != StatusExpectedVerified {
		t.Errorf("positive-only gap + wire present should be expected_verified, got %q", got)
	}
}
