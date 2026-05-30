package fact

import "sort"

// Merge folds expectations + observations into per-fact FactResult bundles,
// computing each fact's status under the strict rules from the design review.
//
// Determinism: results are sorted by (Kind, Runtime, Name) so output is stable.
func Merge(expectations []Expectation, observations []Observation) []FactResult {
	// Index expectations and observations by key.
	exp := make(map[FactKey]Expectation, len(expectations))
	for _, e := range expectations {
		exp[e.Key] = e
	}
	obs := make(map[FactKey][]Observation)
	keys := map[FactKey]struct{}{}
	for _, o := range observations {
		obs[o.Key] = append(obs[o.Key], o)
		keys[o.Key] = struct{}{}
	}
	for k := range exp {
		keys[k] = struct{}{}
	}

	var out []FactResult
	for k := range keys {
		fr := FactResult{Key: k, Observations: obs[k]}
		if e, ok := exp[k]; ok {
			ec := e
			fr.Expectation = &ec
		}
		fr.Status, fr.BestLevel = classify(fr.Expectation, fr.Observations)
		out = append(out, fr)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Key, out[j].Key
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Runtime != b.Runtime {
			return a.Runtime < b.Runtime
		}
		return a.Name < b.Name
	})
	return out
}

// classify computes the status + best evidence level for one fact.
func classify(exp *Expectation, obs []Observation) (Status, *Level) {
	// 1) CONFLICT first — it outranks everything. Requires two observations that
	//    are genuinely comparable (same epoch, both Complete coverage) and
	//    disagree on polarity.
	if conflict(obs) {
		return StatusConflict, bestLevel(obs)
	}

	// 2) Strongest PRESENT observation wins the "expected_*" verdicts.
	if best, ok := strongestPresent(obs); ok {
		lvl := best.Level
		if best.Level == Verified {
			return StatusExpectedVerified, &lvl
		}
		// Observed-present. If it wasn't expected, it's a (benign) surprise.
		if exp == nil {
			return StatusUnexpected, &lvl
		}
		return StatusExpectedObserved, &lvl
	}

	// 3) No present observation. Can we legitimately call it MISSING? Only if a
	//    Complete-coverage source asserted Absent. PositiveOnly absence proves
	//    nothing (the rule that kills false drift).
	if exp != nil {
		for _, o := range obs {
			if o.Polarity == Absent && o.Coverage == CoverageComplete {
				lvl := o.Level
				return StatusMissingExpected, &lvl
			}
		}
		// Expected but nobody could prove presence or a complete-coverage absence.
		return StatusCoverageGap, nil
	}

	return StatusUnknown, nil
}

// conflict reports whether two retained observations are comparable AND disagree.
func conflict(obs []Observation) bool {
	for i := 0; i < len(obs); i++ {
		for j := i + 1; j < len(obs); j++ {
			a, b := obs[i], obs[j]
			if a.Coverage != CoverageComplete || b.Coverage != CoverageComplete {
				continue // a non-complete source cannot anchor a conflict
			}
			if !a.Epoch.Comparable(b.Epoch) {
				continue
			}
			if a.Polarity != b.Polarity {
				return true
			}
		}
	}
	return false
}

// strongestPresent returns the highest-Level Present observation, preferring
// Verified over Observed. Heuristic matches are demoted: they may back Observed
// but never Verified (enforced by callers setting Level appropriately, and here
// we still surface them as Observed-present).
func strongestPresent(obs []Observation) (Observation, bool) {
	var best Observation
	found := false
	for _, o := range obs {
		if o.Polarity != Present {
			continue
		}
		if !found || o.Level > best.Level {
			best, found = o, true
		}
	}
	return best, found
}

func bestLevel(obs []Observation) *Level {
	var lvl *Level
	for _, o := range obs {
		l := o.Level
		if lvl == nil || l > *lvl {
			lvl = &l
		}
	}
	return lvl
}
