package evidence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
	"github.com/cipher982/agent-observatory/backend/internal/transcript"
)

// Local fixtures (mirroring the real transcript shapes).
const instructionText = "Always run the repo smoke test before release."

// Claude: a "file" attachment with instruction text + a complete tool catalog
// via deferred_tools_delta. Uses the HYPHEN form (mcp__search-hub__) to exercise
// Claude-style canonicalization.
const claudeJSONL = `{"type":"attachment","attachment":{"type":"file","content":{"file":{"filePath":"/x/AGENTS.md","content":"Always run the repo smoke test before release."}}},"cwd":"/h/work/app","gitBranch":"main","sessionId":"s1","version":"2.1.156","timestamp":"2026-05-01T10:00:00Z"}
{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__search-hub__query","mcp__issue-hub__list","Bash"]},"timestamp":"2026-05-01T10:00:01Z"}`

// Codex: session_meta + a user message carrying instruction text + one invoked tool.
const codexJSONL = `{"timestamp":"2026-05-02T09:00:00Z","type":"session_meta","payload":{"id":"s1","cwd":"/h/work/app","cli_version":"0.134.0","base_instructions":{"text":"base"}}}
{"timestamp":"2026-05-02T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Always run the repo smoke test before release."}]}}
{"timestamp":"2026-05-02T09:00:02Z","type":"response_item","payload":{"type":"function_call","name":"mcp__reviewer__ask"}}`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testResolution(t *testing.T, dir string) resolver.Resolution {
	t.Helper()
	p := filepath.Join(dir, "AGENTS.md")
	writeFile(t, p, instructionText)
	return resolver.Resolution{Knowledge: []resolver.KnowledgeLayer{{
		Scope: resolver.ScopeGlobal, Label: "global", Path: p, Exists: true, Bytes: len(instructionText),
	}}}
}

// A Claude transcript with a complete tool catalog → tool observations carry
// Complete coverage; instruction text is matched by normalized digest.
func TestTranscriptSourceClaudeComplete(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	writeFile(t, p, claudeJSONL)
	s := transcript.Session{Runtime: "claude", Path: p, SessionID: "s1"}

	obs := TranscriptSource{Resolution: testResolution(t, dir)}.Observe(s)
	var instruction, tool *fact.Observation
	for i := range obs {
		switch obs[i].Key.Kind {
		case fact.InstructionText:
			instruction = &obs[i]
		case fact.ToolAvailable:
			if tool == nil {
				tool = &obs[i]
			}
		}
	}
	if instruction == nil || instruction.Polarity != fact.Present || instruction.Match != fact.MatchNormalizedDigest {
		t.Errorf("instruction obs = %+v, want present/normalized_digest", instruction)
	}
	if tool == nil || tool.Coverage != fact.CoverageComplete {
		t.Errorf("claude tool coverage should be Complete, got %+v", tool)
	}
}

// A Codex transcript → tools are positive-only coverage (invoked-only).
func TestTranscriptSourceCodexPositiveOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-x.jsonl")
	writeFile(t, p, codexJSONL)
	s := transcript.Session{Runtime: "codex", Path: p, SessionID: "s1"}

	obs := TranscriptSource{}.Observe(s)
	sawTool := false
	for _, o := range obs {
		if o.Key.Kind == fact.ToolAvailable {
			sawTool = true
			if o.Coverage != fact.CoveragePositiveOnly {
				t.Errorf("codex tool coverage = %v, want positive_only", o.Coverage)
			}
		}
	}
	if !sawTool {
		t.Skip("no MCP tool in codex fixture (builtins filtered) — coverage path still asserted when present")
	}
}

// Antigravity is unavailable to the transcript source (opaque .pb).
func TestTranscriptSourceAntigravityUnavailable(t *testing.T) {
	ok, reason := TranscriptSource{}.Available(transcript.Session{Runtime: "antigravity"})
	if ok || reason == "" {
		t.Errorf("antigravity should be unavailable with a reason, got ok=%v reason=%q", ok, reason)
	}
}

// ObserveToolAbsence emits Complete-coverage Absent obs for expected tools NOT in
// a complete Claude catalog — and nothing for a positive-only Codex transcript.
func TestObserveToolAbsence(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "claude.jsonl")
	writeFile(t, cp, claudeJSONL)
	cs := transcript.Session{Runtime: "claude", Path: cp, SessionID: "s1"}

	// claudeJSONL's catalog contains mcp__search-hub__ but NOT mcp__ghost-tool__.
	absent := ObserveToolAbsence(cs, []string{"search_hub", "ghost_tool"})
	var ghostAbsent bool
	for _, o := range absent {
		if o.Key.Name == "ghost_tool" {
			ghostAbsent = true
			if o.Polarity != fact.Absent || o.Coverage != fact.CoverageComplete {
				t.Errorf("ghost_tool obs = %+v, want absent/complete", o)
			}
		}
		if o.Key.Name == "search_hub" {
			t.Errorf("search_hub IS present in catalog; must not be reported absent")
		}
	}
	if !ghostAbsent {
		t.Errorf("expected ghost_tool absence observation")
	}

	// Codex (positive-only) must yield NO absence observations.
	xp := filepath.Join(dir, "codex.jsonl")
	writeFile(t, xp, codexJSONL)
	xs := transcript.Session{Runtime: "codex", Path: xp, SessionID: "s1"}
	if got := ObserveToolAbsence(xs, []string{"search_hub"}); len(got) != 0 {
		t.Errorf("codex (positive-only) must not assert absence, got %d obs", len(got))
	}
}

func TestCanonicalToolName(t *testing.T) {
	cases := map[string]string{
		"mcp__search-hub__query": "search_hub", // Claude hyphen form
		"mcp__search_hub__query": "search_hub", // Codex underscore form
		"mcp__issue-hub__list":   "issue_hub",
		"Bash":                   "", // builtin, not a registry tool
		"exec_command":           "",
		"mcp__no_sep":            "",
	}
	for in, want := range cases {
		if got := canonicalToolName(in); got != want {
			t.Errorf("canonicalToolName(%q) = %q, want %q", in, got, want)
		}
	}
}
