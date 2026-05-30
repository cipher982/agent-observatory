package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Claude fixtures ---

// claudeJSONLFor builds a claude transcript whose cwd sits under the given home,
// so GitRepo resolves via the ~/git heuristic.
func claudeJSONLFor(home string) string {
	cwd := filepath.Join(home, "git", "workspace", "example-service")
	return `{"type":"attachment","attachment":{"type":"file","content":{"file":{"filePath":"/x/AGENTS.md","content":"doctrine with Behavior gates inside"}}},"cwd":"` + cwd + `","gitBranch":"main","sessionId":"sess-abc","version":"2.1.152","timestamp":"2026-05-01T10:00:00Z"}
{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__search_hub__query","Bash"]},"timestamp":"2026-05-01T10:00:01Z"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"text"}]},"timestamp":"2026-05-01T10:00:05Z"}
not valid json — must be skipped
{"type":"user","timestamp":"2026-05-01T10:00:10Z"}
`
}

func setupClaude(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// directory name is encoded cwd (irrelevant to parsing).
	p := filepath.Join(home, ".claude", "projects", "-h-git-workspace-example-service", "sess-abc.jsonl")
	mkfile(t, p, claudeJSONLFor(home))
	return home
}

func TestDiscoverClaude(t *testing.T) {
	home := setupClaude(t)
	sessions, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	var cs *Session
	for i := range sessions {
		if sessions[i].Runtime == "claude" {
			cs = &sessions[i]
		}
	}
	if cs == nil {
		t.Fatalf("no claude session discovered, got %d sessions", len(sessions))
	}
	if cs.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want sess-abc", cs.SessionID)
	}
	wantCWD := filepath.Join(home, "git", "workspace", "example-service")
	if cs.CWD != wantCWD {
		t.Errorf("CWD = %q, want %q", cs.CWD, wantCWD)
	}
	if cs.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want main", cs.GitBranch)
	}
	if cs.Version != "2.1.152" {
		t.Errorf("Version = %q", cs.Version)
	}
	if cs.GitRepo != "workspace" {
		t.Errorf("GitRepo = %q, want workspace (under ~/git)", cs.GitRepo)
	}
	// 4 valid records (the malformed line is skipped).
	if cs.RecordCount != 4 {
		t.Errorf("RecordCount = %d, want 4", cs.RecordCount)
	}
}

func TestExtractClaudeContext(t *testing.T) {
	setupClaude(t)
	sessions, _ := Discover()
	var cs Session
	for _, s := range sessions {
		if s.Runtime == "claude" {
			cs = s
		}
	}
	blocks, tools, err := ExtractAssembledContext(cs)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "Behavior gates") {
		t.Errorf("expected marker in blocks, got %q", joined)
	}
	// tools: from deferred delta + tool_use, deduped.
	want := map[string]bool{"mcp__search_hub__query": true, "Bash": true, "Read": true}
	if len(tools) != len(want) {
		t.Errorf("tools = %v, want %v keys", tools, want)
	}
	for _, tn := range tools {
		if !want[tn] {
			t.Errorf("unexpected tool %q", tn)
		}
	}
}

// --- Codex fixtures ---

const codexJSONL = `{"timestamp":"2026-05-02T09:00:00Z","type":"session_meta","payload":{"id":"codex-123","cwd":"/h/git/me","cli_version":"0.134.0","base_instructions":{"text":"codex base prompt"}}}
{"timestamp":"2026-05-02T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"AGENTS.md says Behavior gates matter"}]}}
{"timestamp":"2026-05-02T09:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell"}}
{"timestamp":"2026-05-02T09:00:03Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch"}}
garbage line skipped
`

func setupCodex(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".codex", "sessions", "2026", "05", "02", "rollout-2026-05-02T09-00-00-codex-123.jsonl")
	mkfile(t, p, codexJSONL)
	return home
}

func TestDiscoverCodex(t *testing.T) {
	setupCodex(t)
	sessions, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	var cs *Session
	for i := range sessions {
		if sessions[i].Runtime == "codex" {
			cs = &sessions[i]
		}
	}
	if cs == nil {
		t.Fatalf("no codex session discovered")
	}
	if cs.SessionID != "codex-123" {
		t.Errorf("SessionID = %q, want codex-123", cs.SessionID)
	}
	if cs.CWD != "/h/git/me" {
		t.Errorf("CWD = %q", cs.CWD)
	}
	if cs.Version != "0.134.0" {
		t.Errorf("Version = %q", cs.Version)
	}
	if cs.GitRepo != "me" {
		t.Errorf("GitRepo = %q, want me", cs.GitRepo)
	}
	if cs.RecordCount != 4 {
		t.Errorf("RecordCount = %d, want 4", cs.RecordCount)
	}
}

func TestExtractCodexContext(t *testing.T) {
	setupCodex(t)
	sessions, _ := Discover()
	var cs Session
	for _, s := range sessions {
		if s.Runtime == "codex" {
			cs = s
		}
	}
	blocks, tools, err := ExtractAssembledContext(cs)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "Behavior gates") {
		t.Errorf("expected marker in codex blocks, got %q", joined)
	}
	if !strings.Contains(joined, "codex base prompt") {
		t.Errorf("expected base_instructions in blocks")
	}
	want := map[string]bool{"shell": true, "apply_patch": true}
	if len(tools) != 2 {
		t.Errorf("tools = %v, want shell+apply_patch", tools)
	}
	for _, tn := range tools {
		if !want[tn] {
			t.Errorf("unexpected codex tool %q", tn)
		}
	}
}

// --- Antigravity fixtures ---
//
// Real layout: ~/.gemini/antigravity-cli/conversations/<uuid>.pb (opaque) +
// history.jsonl mapping conversationId -> {workspace, timestamp(ms)}.

// antigravityHistoryJSONL: two history records for conv "conv-1" in /h/git/me.
const antigravityHistoryJSONL = `{"display":"hi","timestamp":1779480000000,"workspace":"/h/git/me","conversationId":"conv-1"}
{"display":"more","timestamp":1779490000000,"workspace":"/h/git/me","conversationId":"conv-1"}`

func setupAntigravity(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".gemini", "antigravity-cli")
	// opaque conversation file — content is irrelevant (never read)
	mkfile(t, filepath.Join(base, "conversations", "conv-1.pb"), "\x00\x01\x02opaque-bytes")
	mkfile(t, filepath.Join(base, "history.jsonl"), antigravityHistoryJSONL)
	return home
}

func TestDiscoverAntigravity(t *testing.T) {
	setupAntigravity(t)
	sessions, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	var as *Session
	for i := range sessions {
		if sessions[i].Runtime == "antigravity" {
			as = &sessions[i]
		}
	}
	if as == nil {
		t.Fatalf("no antigravity session discovered")
	}
	if as.SessionID != "conv-1" {
		t.Errorf("SessionID = %q, want conv-1", as.SessionID)
	}
	if as.CWD != "/h/git/me" {
		t.Errorf("CWD = %q (from history.jsonl)", as.CWD)
	}
	if as.GitRepo != "me" {
		t.Errorf("GitRepo = %q, want me", as.GitRepo)
	}
	if as.RecordCount != 2 {
		t.Errorf("RecordCount = %d, want 2 (history entries)", as.RecordCount)
	}
	// LastActivity should come from the newest history timestamp (1779490000000).
	want := time.UnixMilli(1779490000000)
	if !as.LastActivity.Equal(want) {
		t.Errorf("LastActivity = %v, want %v (newest history ts)", as.LastActivity, want)
	}
}

func TestExtractAntigravityContextEmpty(t *testing.T) {
	setupAntigravity(t)
	sessions, _ := Discover()
	var as Session
	for _, s := range sessions {
		if s.Runtime == "antigravity" {
			as = s
		}
	}
	blocks, tools, err := ExtractAssembledContext(as)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 || len(tools) != 0 {
		t.Errorf("antigravity extract must be empty (opaque .pb), got blocks=%v tools=%v", blocks, tools)
	}
}

// TestDiscoverAllRuntimesSorted: all three runtimes under one HOME, sorted by
// LastActivity descending (antigravity newest via history ts, then codex 05-02,
// then claude 05-01).
func TestDiscoverAllRuntimesSorted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkfile(t, filepath.Join(home, ".claude", "projects", "p", "c.jsonl"), claudeJSONLFor(home))
	mkfile(t, filepath.Join(home, ".codex", "sessions", "2026", "05", "02", "rollout-x-codex-123.jsonl"), codexJSONL)
	base := filepath.Join(home, ".gemini", "antigravity-cli")
	mkfile(t, filepath.Join(base, "conversations", "conv-1.pb"), "opaque")
	// history ts 1779490000000 ms = 2026-05-21, newer than the 05-01/05-02 fixtures
	mkfile(t, filepath.Join(base, "history.jsonl"), antigravityHistoryJSONL)

	sessions, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d: %+v", len(sessions), sessions)
	}
	for i := 1; i < len(sessions); i++ {
		if sessions[i-1].LastActivity.Before(sessions[i].LastActivity) {
			t.Errorf("sessions not sorted desc by LastActivity: %v", sessions)
		}
	}
	if sessions[0].Runtime != "antigravity" {
		t.Errorf("newest should be antigravity, got %q", sessions[0].Runtime)
	}
}

// TestRepoFromCWD exercises the heuristic directly.
func TestRepoFromCWD(t *testing.T) {
	home := "/h"
	cases := []struct{ cwd, want string }{
		{"/h/git/workspace/example-service", "workspace"},
		{"/h/git/me", "me"},
		// cwd exactly at the git root: the trailing-slash prefix check fails to
		// strip cleanly and IndexRune hits the leading separator, yielding "".
		// Documented quirk, not exercised by real runtimes (cwd is always a repo).
		{"/h/git", ""},
		{"/tmp/elsewhere/proj", "proj"},
		{"", ""},
	}
	for _, c := range cases {
		if got := repoFromCWD(c.cwd, home); got != c.want {
			t.Errorf("repoFromCWD(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
}

// TestDiscoverEmptyHome: a HOME with no runtime dirs yields no sessions, no error.
func TestDiscoverEmptyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessions, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions in empty home, got %d", len(sessions))
	}
}

// TestIntegrationRealHome optionally reads David's real ~ and skips if absent.
// Deterministic suites never depend on this.
func TestIntegrationRealHome(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-home integration scan in -short mode")
	}
	real, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if _, err := os.Stat(filepath.Join(real, ".claude", "projects")); err != nil {
		t.Skip("no real ~/.claude/projects; skipping integration scan")
	}
	// Note: do NOT t.Setenv here — we want the real home.
	sessions, err := Discover()
	if err != nil {
		t.Fatalf("Discover on real home errored: %v", err)
	}
	t.Logf("discovered %d real sessions", len(sessions))
	for _, s := range sessions {
		if s.Runtime == "" || s.Path == "" {
			t.Errorf("malformed session: %+v", s)
		}
	}
}
