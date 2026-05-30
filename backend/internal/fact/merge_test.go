package fact

import "testing"

func key(kind Kind, rt, name string) FactKey {
	return FactKey{Kind: kind, Runtime: rt, Name: name}
}

func toolKey(rt, name string) FactKey { return key(ToolAvailable, rt, name) }

// helper builders
func expect(k FactKey) Expectation { return Expectation{Key: k, Required: true, Origin: "global"} }
func obsPresent(k FactKey, lvl Level, src string, cov Coverage, ep Epoch) Observation {
	return Observation{Key: k, Polarity: Present, Level: lvl, Source: src, Coverage: cov, Epoch: ep}
}
func obsAbsent(k FactKey, lvl Level, src string, cov Coverage, ep Epoch) Observation {
	return Observation{Key: k, Polarity: Absent, Level: lvl, Source: src, Coverage: cov, Epoch: ep}
}

func statusFor(results []FactResult, k FactKey) Status {
	for _, r := range results {
		if r.Key == k {
			return r.Status
		}
	}
	return ""
}

// Expected + transcript-observed present → expected_observed.
func TestExpectedObserved(t *testing.T) {
	k := toolKey("claude", "slack-hub")
	r := Merge([]Expectation{expect(k)},
		[]Observation{obsPresent(k, Observed, "transcript", CoverageComplete, Epoch{SessionID: "s1"})})
	if got := statusFor(r, k); got != StatusExpectedObserved {
		t.Errorf("status = %q, want expected_observed", got)
	}
}

// Expected + wire-verified present → expected_verified, best level Verified.
func TestExpectedVerified(t *testing.T) {
	k := toolKey("codex", "hatch")
	r := Merge([]Expectation{expect(k)},
		[]Observation{obsPresent(k, Verified, "wire", CoverageComplete, Epoch{SessionID: "s1", RequestID: "r1"})})
	fr := r[0]
	if fr.Status != StatusExpectedVerified {
		t.Errorf("status = %q, want expected_verified", fr.Status)
	}
	if fr.BestLevel == nil || *fr.BestLevel != Verified {
		t.Errorf("best level = %v, want verified", fr.BestLevel)
	}
}

// THE false-drift guard: a PositiveOnly source (Codex tools) that does NOT
// mention an expected tool must NOT produce missing_expected — it's a coverage
// gap, because absence proves nothing for positive-only sources.
func TestPositiveOnlyNoFalseMissing(t *testing.T) {
	k := toolKey("codex", "slack-hub")
	// transcript saw OTHER tools but not slack-hub; positive-only means we record
	// no observation for slack-hub at all (absence isn't asserted).
	r := Merge([]Expectation{expect(k)}, nil)
	if got := statusFor(r, k); got != StatusCoverageGap {
		t.Errorf("status = %q, want coverage_gap (positive-only absence is not drift)", got)
	}
}

// A Complete-coverage source asserting Absent for an expected fact → missing_expected.
func TestCompleteAbsenceIsMissing(t *testing.T) {
	k := toolKey("claude", "stripe-mcp")
	r := Merge([]Expectation{expect(k)},
		[]Observation{obsAbsent(k, Observed, "transcript", CoverageComplete, Epoch{SessionID: "s1"})})
	if got := statusFor(r, k); got != StatusMissingExpected {
		t.Errorf("status = %q, want missing_expected", got)
	}
}

// CONFLICT: transcript says present, wire says absent, SAME epoch, BOTH complete.
func TestConflictSameEpochBothComplete(t *testing.T) {
	k := key(InstructionText, "claude", "AGENTS.md")
	ep := Epoch{SessionID: "s1", RequestID: "r1"}
	r := Merge([]Expectation{expect(k)}, []Observation{
		obsPresent(k, Observed, "transcript", CoverageComplete, ep),
		obsAbsent(k, Verified, "wire", CoverageComplete, ep),
	})
	if got := statusFor(r, k); got != StatusConflict {
		t.Errorf("status = %q, want conflict", got)
	}
}

// NOT a conflict: disagreement but one source is positive-only (can't anchor).
func TestNoConflictWhenNotComplete(t *testing.T) {
	k := toolKey("codex", "longhouse")
	ep := Epoch{SessionID: "s1"}
	r := Merge([]Expectation{expect(k)}, []Observation{
		obsPresent(k, Observed, "transcript", CoveragePositiveOnly, ep),
		obsAbsent(k, Verified, "wire", CoverageComplete, ep),
	})
	// present (positive-only) wins as expected_verified? No: present is Observed,
	// absent is Verified+Complete. strongestPresent picks the Observed-present, so
	// it resolves to expected_observed — NOT a conflict (positive-only can't anchor).
	if got := statusFor(r, k); got == StatusConflict {
		t.Errorf("status = conflict, but positive-only must not anchor a conflict")
	}
}

// NOT a conflict: disagreement across DIFFERENT epochs (different requests).
func TestNoConflictDifferentEpoch(t *testing.T) {
	k := key(InstructionText, "claude", "AGENTS.md")
	r := Merge([]Expectation{expect(k)}, []Observation{
		obsPresent(k, Observed, "transcript", CoverageComplete, Epoch{SessionID: "s1", RequestID: "r1"}),
		obsAbsent(k, Verified, "wire", CoverageComplete, Epoch{SessionID: "s1", RequestID: "r2"}),
	})
	if got := statusFor(r, k); got == StatusConflict {
		t.Errorf("different-epoch disagreement must NOT be a conflict")
	}
}

// Observed present but never expected → unexpected (benign surprise, not drift).
func TestUnexpected(t *testing.T) {
	k := toolKey("claude", "mystery-tool")
	r := Merge(nil, []Observation{obsPresent(k, Observed, "transcript", CoverageComplete, Epoch{SessionID: "s1"})})
	if got := statusFor(r, k); got != StatusUnexpected {
		t.Errorf("status = %q, want unexpected", got)
	}
}

// Verified beats Observed for the same present fact.
func TestVerifiedBeatsObserved(t *testing.T) {
	k := toolKey("codex", "hatch")
	ep := Epoch{SessionID: "s1"}
	r := Merge([]Expectation{expect(k)}, []Observation{
		obsPresent(k, Observed, "transcript", CoverageComplete, ep),
		obsPresent(k, Verified, "wire", CoverageComplete, ep),
	})
	fr := r[0]
	if fr.Status != StatusExpectedVerified || fr.BestLevel == nil || *fr.BestLevel != Verified {
		t.Errorf("status=%q best=%v, want expected_verified/verified", fr.Status, fr.BestLevel)
	}
}

// Epoch.Comparable rules.
func TestEpochComparable(t *testing.T) {
	cases := []struct {
		a, b Epoch
		want bool
	}{
		{Epoch{SessionID: "s1"}, Epoch{SessionID: "s1"}, true},
		{Epoch{SessionID: "s1"}, Epoch{SessionID: "s2"}, false},
		{Epoch{}, Epoch{SessionID: "s1"}, false},
		{Epoch{SessionID: "s1", RequestID: "r1"}, Epoch{SessionID: "s1", RequestID: "r1"}, true},
		{Epoch{SessionID: "s1", RequestID: "r1"}, Epoch{SessionID: "s1", RequestID: "r2"}, false},
		{Epoch{SessionID: "s1", RequestID: "r1"}, Epoch{SessionID: "s1"}, true}, // fall back to session
	}
	for _, c := range cases {
		if got := c.a.Comparable(c.b); got != c.want {
			t.Errorf("Comparable(%+v,%+v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
