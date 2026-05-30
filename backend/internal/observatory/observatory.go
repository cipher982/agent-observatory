// Package observatory is the shared view layer over the resolver, transcript,
// evidence, and fact packages. It returns plain, JSON-marshalable structs so the
// CLI and the GUI consume exactly the same data, computed exactly once, here.
//
//   - ExplainPath: the effective resolved context for a single path (read-only).
//   - LiveSessions: discovered agent sessions joined with their resolved context
//     and the fact-level evidence model (EXPECTED/OBSERVED/VERIFIED witness marks).
package observatory

import (
	"github.com/cipher982/agent-observatory/backend/internal/evidence"
	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
	"github.com/cipher982/agent-observatory/backend/internal/transcript"
)

// defaultSessionLimit caps LiveSessions when the caller passes limit <= 0.
const defaultSessionLimit = 25

// SessionView joins one discovered session with its resolved context and the
// fact-level evidence results. SummaryLevel is a lossy badge ("none" | "observed"
// | "verified") — the per-fact Facts are the source of truth.
type SessionView struct {
	Session      transcript.Session `json:"session"`
	Workspace    string             `json:"workspace"`
	SummaryLevel string             `json:"summaryLevel"`
	Facts        []fact.FactResult  `json:"facts"`
	ActiveSkills []string           `json:"activeSkills"`
	ActiveTools  []string           `json:"activeTools"`
	// SourceStatus reports per-evidence-source availability (honest "why not").
	SourceStatus []SourceStatus `json:"sourceStatus"`
}

// SourceStatus is one evidence source's availability for a session.
type SourceStatus struct {
	Source    string `json:"source"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// ExplainPath resolves the effective agent context for a filesystem path.
func ExplainPath(path string) (resolver.Resolution, error) {
	return resolver.LoadFromDisk(path)
}

// WireObservations is an optional hook: a capture backend (Phase D) can register
// wire-derived observations keyed by session ID. LiveSessions folds them in as
// VERIFIED evidence. Nil when no wire capture is active.
var WireObservations func(sessionID string) []fact.Observation

// LiveSessions discovers recent agent sessions and runs the fact-level evidence
// pipeline for each: resolver Expectations vs transcript (OBSERVED) + optional
// wire (VERIFIED) Observations, merged into FactResults.
func LiveSessions(limit int) ([]SessionView, error) {
	if limit <= 0 {
		limit = defaultSessionLimit
	}
	sessions, err := transcript.DiscoverRecent(limit)
	if err != nil {
		return nil, err
	}

	resCache := make(map[string]resolver.Resolution)
	resolveCWD := func(cwd string) resolver.Resolution {
		if r, ok := resCache[cwd]; ok {
			return r
		}
		r, rerr := resolver.LoadFromDisk(cwd)
		if rerr != nil {
			r = resolver.Resolution{Path: cwd}
		}
		resCache[cwd] = r
		return r
	}

	tsrc := evidence.TranscriptSource{}
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		res := resolveCWD(s.CWD)
		views = append(views, buildView(s, res, tsrc))
	}
	return views, nil
}

// buildView runs the evidence pipeline for one session.
func buildView(s transcript.Session, res resolver.Resolution, tsrc evidence.TranscriptSource) SessionView {
	expectations := evidence.ExpectationsFromResolution(s.Runtime, res)

	var observations []fact.Observation
	var sourceStatus []SourceStatus

	// Transcript (OBSERVED) source.
	if ok, reason := tsrc.Available(s); ok {
		observations = append(observations, tsrc.Observe(s)...)
		// Complete-catalog absence (real drift) needs the expected tool set.
		observations = append(observations, evidence.ObserveToolAbsence(s, expectedToolNames(expectations))...)
		sourceStatus = append(sourceStatus, SourceStatus{Source: "transcript", Available: true})
	} else {
		sourceStatus = append(sourceStatus, SourceStatus{Source: "transcript", Available: false, Reason: reason})
	}

	// Wire (VERIFIED) source, if a capture backend is registered.
	if WireObservations != nil {
		if wireObs := WireObservations(s.SessionID); len(wireObs) > 0 {
			observations = append(observations, wireObs...)
			sourceStatus = append(sourceStatus, SourceStatus{Source: "wire", Available: true})
		} else {
			sourceStatus = append(sourceStatus, SourceStatus{Source: "wire", Available: false, Reason: "no wire capture for this session"})
		}
	}

	facts := fact.Merge(expectations, observations)
	return SessionView{
		Session:      s,
		Workspace:    res.Workspace,
		SummaryLevel: summaryLevel(facts),
		Facts:        facts,
		ActiveSkills: resolver.ActiveNames(res.Skills),
		ActiveTools:  resolver.ActiveNames(res.Tools),
		SourceStatus: sourceStatus,
	}
}

// expectedToolNames extracts the canonical tool names from tool expectations.
func expectedToolNames(exps []fact.Expectation) []string {
	var out []string
	for _, e := range exps {
		if e.Key.Kind == fact.ToolAvailable {
			out = append(out, e.Key.Name)
		}
	}
	return out
}

// summaryLevel is the lossy session badge: highest evidence level any fact reached.
func summaryLevel(facts []fact.FactResult) string {
	best := "none"
	for _, f := range facts {
		if f.BestLevel == nil {
			continue
		}
		switch *f.BestLevel {
		case fact.Verified:
			return "verified" // can't beat this
		case fact.Observed:
			best = "observed"
		}
	}
	return best
}
