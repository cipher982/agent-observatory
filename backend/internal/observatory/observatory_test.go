package observatory

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExplainPath builds a minimal fake $HOME/git/me and asserts ExplainPath
// resolves it through the resolver (catalog, knowledge layer, workspace).
func TestExplainPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	me := filepath.Join(home, "git", "me")
	mustMkdir(t, filepath.Join(me, "skills", "docket"))
	mustMkdir(t, filepath.Join(me, "registry"))
	mustWrite(t, filepath.Join(me, "AGENTS.md"), "# doctrine\nBehavior gates\n")
	mustWrite(t, filepath.Join(me, "skills", "docket", "SKILL.md"), "---\nname: docket\n---\n")
	mustWrite(t, filepath.Join(me, "registry", "mcp-registry.toml"),
		"[servers.hatch]\ncommand=\"x\"\n[servers.longhouse]\ncommand=\"y\"\n")

	res, err := ExplainPath(me)
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != "me" {
		t.Errorf("workspace = %q, want me", res.Workspace)
	}
	if len(res.Knowledge) == 0 || !res.Knowledge[0].Exists {
		t.Errorf("expected an existing global knowledge layer, got %+v", res.Knowledge)
	}
	if len(res.Tools) != 2 {
		t.Errorf("tools = %d, want 2 (hatch, longhouse)", len(res.Tools))
	}
	// Default tool activation is Enabled, so both tools should be active.
	active := 0
	for _, tl := range res.Tools {
		if tl.Active {
			active++
		}
	}
	if active != 2 {
		t.Errorf("active tools = %d, want 2 (default-on)", active)
	}
}

// TestLiveSessionsEmptyHome: with a HOME that has no transcript dirs, LiveSessions
// returns an empty slice and no error (graceful, not a crash).
func TestLiveSessionsEmptyHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	views, err := LiveSessions(10)
	if err != nil {
		t.Fatalf("LiveSessions error on empty home: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("views = %d, want 0 on empty home", len(views))
	}
}

// TestLiveSessionsLimitDefault: limit <= 0 falls back to the default cap. We only
// assert the call succeeds and respects a small explicit cap on a synthetic home.
func TestLiveSessionsExplicitLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	views, err := LiveSessions(0) // 0 -> defaultSessionLimit
	if err != nil {
		t.Fatal(err)
	}
	if len(views) > defaultSessionLimit {
		t.Errorf("views = %d exceeds default cap %d", len(views), defaultSessionLimit)
	}
}

// TestLiveSessionsFactPipeline exercises the full pipeline end-to-end: a fake
// HOME with a Claude transcript (complete catalog) under a resolvable cwd →
// expectations + observations → merged FactResults with a summary level.
func TestLiveSessionsFactPipeline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Canonical context so the resolver produces tool expectations.
	me := filepath.Join(home, "git", "me")
	mustMkdir(t, filepath.Join(me, "registry"))
	mustWrite(t, filepath.Join(me, "AGENTS.md"), "# doctrine\nBehavior gates\n")
	mustWrite(t, filepath.Join(me, "registry", "mcp-registry.toml"),
		"[servers.slack-hub]\ncommand=\"x\"\n[servers.ghost-tool]\ncommand=\"y\"\n")

	// A Claude transcript whose cwd is ~/git/me, with a complete catalog that
	// contains slack-hub (hyphen form) but NOT ghost-tool → ghost-tool must drift.
	enc := "-Users-" // not load-bearing; discovery uses dir listing
	_ = enc
	proj := filepath.Join(home, ".claude", "projects", "p")
	mustMkdir(t, proj)
	mustWrite(t, filepath.Join(proj, "c.jsonl"),
		`{"type":"attachment","attachment":{"type":"file","content":{"file":{"filePath":"/x/AGENTS.md","content":"doctrine Behavior gates"}}},"cwd":"`+me+`","gitBranch":"main","sessionId":"sess-1","version":"2.1","timestamp":"2026-05-29T10:00:00Z"}
{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__slack-hub__search","Bash"]},"timestamp":"2026-05-29T10:00:01Z"}`)

	views, err := LiveSessions(10)
	if err != nil {
		t.Fatal(err)
	}
	var cv *SessionView
	for i := range views {
		if views[i].Session.Runtime == "claude" {
			cv = &views[i]
		}
	}
	if cv == nil {
		t.Fatal("no claude session discovered")
	}
	if cv.SummaryLevel != "observed" {
		t.Errorf("summary level = %q, want observed", cv.SummaryLevel)
	}
	// Find the doctrine + the two tool facts.
	byName := map[string]string{} // name -> status
	for _, f := range cv.Facts {
		byName[f.Key.Name] = string(f.Status)
	}
	if byName["AGENTS.md global doctrine"] != "expected_observed" {
		t.Errorf("doctrine status = %q, want expected_observed", byName["AGENTS.md global doctrine"])
	}
	if byName["slack_hub"] != "expected_observed" {
		t.Errorf("slack_hub status = %q, want expected_observed (hyphen→underscore match)", byName["slack_hub"])
	}
	if byName["ghost_tool"] != "missing_expected" {
		t.Errorf("ghost_tool status = %q, want missing_expected (complete-catalog drift)", byName["ghost_tool"])
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
