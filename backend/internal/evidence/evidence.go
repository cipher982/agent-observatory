// Package evidence adapts concrete sources (transcript files, wire captures) and
// the resolver into the typed fact model. Each EvidenceSource self-declares what
// it can observe for a given session (availability + coverage), then emits
// Observations that Merge folds against the resolver's Expectations.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
	"github.com/cipher982/agent-observatory/backend/internal/transcript"
)

// Source is an evidence producer. Availability lets the UI report honest reasons
// when a source can't speak to a session.
type Source interface {
	Name() string
	// Available reports whether this source can observe the session at all, with
	// a human reason when it cannot.
	Available(s transcript.Session) (ok bool, reason string)
	// Observe emits observations for the session (may be empty).
	Observe(s transcript.Session) []fact.Observation
}

// ExpectationsFromResolution turns resolver-active skills/tools + knowledge into
// typed Expectations. These are the claims under test — NOT evidence.
func ExpectationsFromResolution(runtime string, res resolver.Resolution) []fact.Expectation {
	var out []fact.Expectation
	// Every resolved instruction layer is expected to be present in the assembled
	// prompt. The digest lets observations match the user's own instructions
	// without relying on private marker phrases.
	for _, kl := range res.Knowledge {
		_, digest, ok := knowledgeFingerprint(kl)
		if !ok {
			continue
		}
		out = append(out, fact.Expectation{
			Key: fact.FactKey{
				Kind:    fact.InstructionText,
				Runtime: runtime,
				Name:    instructionFactName(kl),
				Digest:  digest,
			},
			Required: true,
			Origin:   kl.Label,
		})
	}
	// Each resolver-active tool (MCP server) is expected to be available. Names
	// are canonicalized (underscored) so expectation keys match observation keys
	// regardless of the runtime's hyphen/underscore convention.
	for _, it := range res.Tools {
		if !it.Active {
			continue
		}
		out = append(out, fact.Expectation{
			Key:      fact.FactKey{Kind: fact.ToolAvailable, Runtime: runtime, Name: CanonTool(it.Name)},
			Required: true,
			Origin:   it.OriginLabel,
		})
	}
	return out
}

// TranscriptSource emits OBSERVED evidence from the on-disk CLI transcript.
type TranscriptSource struct {
	Resolution resolver.Resolution
}

func (TranscriptSource) Name() string { return "transcript" }

func (TranscriptSource) Available(s transcript.Session) (bool, string) {
	switch s.Runtime {
	case "claude", "codex":
		return true, ""
	case "antigravity":
		return false, "Antigravity stores opaque (encrypted) .pb transcripts — no readable context"
	default:
		return false, "unknown runtime"
	}
}

// Observe reads the assembled context and emits observations. The KEY nuance:
// tool coverage differs per runtime (Claude=complete catalog, Codex=invoked-only
// → positive). Instruction-text presence is matched against the user's resolved
// instruction files by normalized digest, not by a hardcoded marker phrase.
func (ts TranscriptSource) Observe(s transcript.Session) []fact.Observation {
	return ts.ObserveWithResolution(s, ts.Resolution)
}

func (ts TranscriptSource) ObserveWithResolution(s transcript.Session, res resolver.Resolution) []fact.Observation {
	if len(res.Knowledge) == 0 {
		res = ts.Resolution
	}
	asm, err := transcript.ExtractAssembled(s)
	if err != nil {
		return nil
	}
	ep := fact.Epoch{SessionID: s.SessionID}
	var out []fact.Observation

	joined := strings.Join(asm.SystemPromptBlocks, "\n")
	if len(asm.SystemPromptBlocks) > 0 {
		out = append(out, ObserveInstructionText("transcript", fact.Observed, s.Runtime, ep, joined, res, false)...)
	}

	// Tools. Coverage depends on completeness.
	toolCov := fact.CoveragePositiveOnly
	if asm.ToolCatalogComplete {
		toolCov = fact.CoverageComplete
	}
	seen := map[string]bool{}
	for _, raw := range asm.ToolNames {
		server := canonicalToolName(raw)
		if server == "" || seen[server] {
			continue
		}
		seen[server] = true
		out = append(out, fact.Observation{
			Key:      fact.FactKey{Kind: fact.ToolAvailable, Runtime: s.Runtime, Name: server},
			Polarity: fact.Present, Level: fact.Observed,
			Source: "transcript", Coverage: toolCov, Match: fact.MatchToolName, Epoch: ep,
		})
	}
	// Absence of an expected tool (→ missing_expected) is only assertable when the
	// catalog is complete; that's emitted separately via ObserveToolAbsence using
	// the expectation set, so the merge can distinguish "complete-catalog absence"
	// (real drift) from "positive-only didn't mention it" (coverage gap).
	return out
}

// ObserveInstructionText emits observations for every expected instruction layer
// that can be tested against captured prompt text. When complete is false,
// absence is omitted rather than guessed.
func ObserveInstructionText(source string, level fact.Level, runtime string, ep fact.Epoch, text string, res resolver.Resolution, complete bool) []fact.Observation {
	normText := normalizeInstructionText(text)
	if normText == "" {
		return nil
	}
	var out []fact.Observation
	for _, kl := range res.Knowledge {
		normExpected, digest, ok := knowledgeFingerprint(kl)
		if !ok {
			continue
		}
		key := fact.FactKey{Kind: fact.InstructionText, Runtime: runtime, Name: instructionFactName(kl), Digest: digest}
		if strings.Contains(normText, normExpected) {
			out = append(out, fact.Observation{
				Key: key, Polarity: fact.Present, Level: level, Source: source,
				Coverage: fact.CoverageComplete, Match: fact.MatchNormalizedDigest, Epoch: ep,
				Detail: "resolved instruction text present in captured prompt",
			})
			continue
		}
		if complete {
			out = append(out, fact.Observation{
				Key: key, Polarity: fact.Absent, Level: level, Source: source,
				Coverage: fact.CoverageComplete, Match: fact.MatchNormalizedDigest, Epoch: ep,
				Detail: "resolved instruction text absent from captured prompt",
			})
		}
	}
	return out
}

func instructionFactName(kl resolver.KnowledgeLayer) string {
	if kl.Label == "" || kl.Label == "global" {
		return "AGENTS.md global instructions"
	}
	return "AGENTS.md " + kl.Label
}

func knowledgeFingerprint(kl resolver.KnowledgeLayer) (normalized, digest string, ok bool) {
	if !kl.Exists {
		return "", "", false
	}
	data, err := os.ReadFile(kl.Path)
	if err != nil {
		return "", "", false
	}
	normalized = normalizeInstructionText(string(data))
	if normalized == "" {
		return "", "", false
	}
	sum := sha256.Sum256([]byte(normalized))
	return normalized, hex.EncodeToString(sum[:]), true
}

func normalizeInstructionText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ObserveToolAbsence emits Absent observations (Complete coverage) for expected
// tools that a COMPLETE-catalog transcript did not contain. This is what lets the
// merge produce missing_expected for genuine Claude drift, while never doing so
// for Codex (positive-only). Call only when the source's tool coverage is complete.
func ObserveToolAbsence(s transcript.Session, expectedTools []string) []fact.Observation {
	asm, err := transcript.ExtractAssembled(s)
	if err != nil || !asm.ToolCatalogComplete {
		return nil
	}
	ep := fact.Epoch{SessionID: s.SessionID}
	present := map[string]bool{}
	for _, raw := range asm.ToolNames {
		if c := canonicalToolName(raw); c != "" {
			present[c] = true
		}
	}
	var out []fact.Observation
	for _, t := range expectedTools {
		if !present[t] {
			out = append(out, fact.Observation{
				Key:      fact.FactKey{Kind: fact.ToolAvailable, Runtime: s.Runtime, Name: t},
				Polarity: fact.Absent, Level: fact.Observed,
				Source: "transcript", Coverage: fact.CoverageComplete, Match: fact.MatchToolName, Epoch: ep,
				Detail: "expected tool absent from complete assembled catalog",
			})
		}
	}
	return out
}

// canonicalToolName maps a raw tool name to its canonical MCP server identity
// (underscored), tolerating Claude's hyphens vs Codex's underscores. Non-MCP
// builtins (Bash, exec_command, etc.) return "" — they are not registry tools.
func canonicalToolName(raw string) string {
	if !strings.HasPrefix(raw, "mcp__") {
		return ""
	}
	rest := strings.TrimPrefix(raw, "mcp__")
	i := strings.Index(rest, "__")
	if i < 0 {
		return ""
	}
	return strings.ReplaceAll(rest[:i], "-", "_")
}

// CanonTool exposes canonicalization for callers building expectation keys so
// both sides of the merge use the same identity.
func CanonTool(name string) string { return strings.ReplaceAll(name, "-", "_") }
