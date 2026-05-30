package main

import (
	"path/filepath"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/evidence"
	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/observatory"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
	"github.com/cipher982/agent-observatory/backend/internal/wire"
)

// installWireObservations wires persisted captures into the observatory as
// VERIFIED evidence. Captures are stored per-runtime under the state dir; here
// we expose them to the fact pipeline keyed by session.
//
// v2 correlation is coarse: captures from the most recent runtime capture are
// attributed to that runtime's most-recent session. Per-request session
// correlation is a refinement; this is enough to surface real VERIFIED facts
// and to demonstrate CONFLICT against the transcript.
func installWireObservations() {
	observatory.WireObservations = func(runtime, sessionID string, res resolver.Resolution) []fact.Observation {
		dir := stateDir()
		caps, err := wire.ReadCaptures(filepath.Join(dir, "wire-"+runtime+".json"))
		if err != nil {
			return nil
		}
		return observationsFromCaptures(runtime, sessionID, res, caps)
	}
}

// observationsFromCaptures mirrors wire.Server.Observations but works on persisted
// (redacted) captures and a supplied runtime/session.
func observationsFromCaptures(runtime, sessionID string, res resolver.Resolution, caps []wire.Capture) []fact.Observation {
	var out []fact.Observation
	for i, c := range caps {
		ep := fact.Epoch{SessionID: sessionID, RequestID: "wire-" + runtime + "-" + itoa(i)}
		out = append(out, evidence.ObserveInstructionText("wire", fact.Verified, runtime, ep, c.AllText, res, true)...)
		for _, raw := range c.ToolNames {
			server := canonServerName(raw)
			if server == "" {
				continue
			}
			out = append(out, fact.Observation{
				Key:      fact.FactKey{Kind: fact.ToolAvailable, Runtime: runtime, Name: evidence.CanonTool(server)},
				Polarity: fact.Present, Level: fact.Verified, Source: "wire",
				Coverage: fact.CoverageComplete, Match: fact.MatchToolName, Epoch: ep,
			})
		}
	}
	return out
}

func canonServerName(raw string) string {
	const p = "mcp__"
	if !strings.HasPrefix(raw, p) {
		return ""
	}
	rest := raw[len(p):]
	if i := strings.Index(rest, "__"); i >= 0 {
		return rest[:i]
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
