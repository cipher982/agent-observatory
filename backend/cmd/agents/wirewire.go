package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/evidence"
	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/observatory"
	"github.com/cipher982/agent-observatory/backend/internal/wire"
)

// installWireObservations wires persisted `agents run` captures into the
// observatory as VERIFIED evidence. Captures are stored per-runtime under the
// state dir; here we expose them to the fact pipeline keyed by session.
//
// v2 correlation is coarse: captures from the most recent `agents run <runtime>`
// are attributed to that runtime's most-recent session. Per-request session
// correlation is a refinement; this is enough to surface real VERIFIED facts and
// to demonstrate CONFLICT against the transcript.
func installWireObservations() {
	observatory.WireObservations = func(sessionID string) []fact.Observation {
		// We don't know the runtime from sessionID alone here, so scan all
		// persisted wire files and emit observations tagged with their runtime;
		// the merge only matches when the FactKey.Runtime aligns with the
		// session's expectations, so cross-runtime captures are naturally ignored.
		dir := stateDir()
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []fact.Observation
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "wire-") || !strings.HasSuffix(name, ".json") {
				continue
			}
			runtime := strings.TrimSuffix(strings.TrimPrefix(name, "wire-"), ".json")
			caps, err := wire.ReadCaptures(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			out = append(out, observationsFromCaptures(runtime, sessionID, caps)...)
		}
		return out
	}
}

// observationsFromCaptures mirrors wire.Server.Observations but works on persisted
// (redacted) captures and a supplied runtime/session.
func observationsFromCaptures(runtime, sessionID string, caps []wire.Capture) []fact.Observation {
	var out []fact.Observation
	for i, c := range caps {
		ep := fact.Epoch{SessionID: sessionID, RequestID: "wire-" + runtime + "-" + itoa(i)}
		dk := fact.FactKey{Kind: fact.InstructionText, Runtime: runtime, Name: "AGENTS.md global doctrine"}
		pol := fact.Absent
		detail := "doctrine marker ABSENT from outbound request"
		if c.AgentsMarker {
			pol = fact.Present
			detail = "doctrine marker present in outbound request (" + c.MarkerSlot + ")"
		}
		out = append(out, fact.Observation{
			Key: dk, Polarity: pol, Level: fact.Verified, Source: "wire",
			Coverage: fact.CoverageComplete, Match: fact.MatchMarkerHeuristic, Epoch: ep, Detail: detail,
		})
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
