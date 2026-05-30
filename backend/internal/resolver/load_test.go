package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates parent dirs and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeHome lays out a minimal legacy ~/git/me layout under a temp dir and sets $HOME.
// Returns the home path.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	me := filepath.Join(home, "git", "me")

	// Global doctrine AGENTS.md
	writeFile(t, filepath.Join(me, "AGENTS.md"), "global instructions for agent behavior\n")

	// Skill catalog: two skills with SKILL.md, one dir without (must be ignored),
	// and a stray file (ignored).
	writeFile(t, filepath.Join(me, "skills", "summarizer", "SKILL.md"), "# summarizer\n")
	writeFile(t, filepath.Join(me, "skills", "reviewer", "SKILL.md"), "# reviewer\n")
	if err := os.MkdirAll(filepath.Join(me, "skills", "empty-no-skillmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(me, "skills", "loose.txt"), "ignore me\n")

	// Tool catalog: mcp-registry.toml with three servers.
	writeFile(t, filepath.Join(me, "registry", "mcp-registry.toml"), `
# registry
[servers.search-hub]
command = "x"

[servers.docs-hub]
command = "y"

[servers.issue-hub]
command = "z"
`)
	return home
}

func TestLoadFromDiskCatalogAndDefaults(t *testing.T) {
	home := fakeHome(t)
	me := filepath.Join(home, "git", "me")

	res, err := LoadFromDisk(me)
	if err != nil {
		t.Fatal(err)
	}

	// Skills: catalog of summarizer+reviewer, default Disabled => none active, both present.
	if len(res.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d: %v", len(res.Skills), res.Skills)
	}
	for _, s := range res.Skills {
		if s.Active {
			t.Errorf("skill %q should be inactive by default (DefaultSkill=Disabled)", s.Name)
		}
	}

	// Tools: catalog of three servers, default Enabled => all active.
	if len(res.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(res.Tools), res.Tools)
	}
	for _, tl := range res.Tools {
		if !tl.Active {
			t.Errorf("tool %q should be active by default (DefaultTool=Enabled)", tl.Name)
		}
	}

	// Global knowledge layer must exist and have nonzero bytes.
	var foundGlobal bool
	for _, kl := range res.Knowledge {
		if kl.Scope == ScopeGlobal {
			foundGlobal = true
			if !kl.Exists {
				t.Errorf("global knowledge layer should exist")
			}
			if kl.Bytes == 0 {
				t.Errorf("global knowledge bytes should be nonzero")
			}
		}
	}
	if !foundGlobal {
		t.Errorf("no global knowledge layer found")
	}
}

// TestLoadFromDiskOverlayPrecedence: a global overlay enables a skill and disables
// a tool; a workspace overlay flips them; a repo overlay flips one again.
func TestLoadFromDiskOverlayPrecedence(t *testing.T) {
	home := fakeHome(t)
	me := filepath.Join(home, "git", "me")

	// Global overlay at ~/git/me/agents.yaml
	writeFile(t, filepath.Join(me, "agents.yaml"), `
skills:
  summarizer: enabled
tools:
  search-hub: disabled
`)

	// A workspace with its own AGENTS.md and overlay.
	workspace := filepath.Join(home, "git", "workspace")
	writeFile(t, filepath.Join(workspace, "AGENTS.md"), "workspace knowledge\n")
	writeFile(t, filepath.Join(workspace, "agents.yaml"), `
skills:
  summarizer: disabled
tools:
  search-hub: enabled
`)

	// A repo under the workspace with its own AGENTS.md + overlay re-enabling the skill.
	repo := filepath.Join(workspace, "example-service")
	writeFile(t, filepath.Join(repo, "AGENTS.md"), "example service repo knowledge\n")
	writeFile(t, filepath.Join(repo, "agents.yaml"), `
skills:
  summarizer: enabled
`)

	res, err := LoadFromDisk(repo)
	if err != nil {
		t.Fatal(err)
	}

	if res.Workspace != "workspace" {
		t.Errorf("Workspace = %q, want workspace", res.Workspace)
	}

	summarizer := findItem(t, res.Skills, "summarizer")
	if !summarizer.Active {
		t.Errorf("summarizer should be re-enabled at repo, got inactive (why=%q)", summarizer.WhyInactive)
	}
	if summarizer.Origin != ScopeRepo {
		t.Errorf("summarizer.Origin = %v, want repo", summarizer.Origin)
	}
	// Override chain: global(enabled) then workspace(disabled).
	if len(summarizer.Overrode) != 2 {
		t.Errorf("summarizer.Overrode len = %d, want 2: %#v", len(summarizer.Overrode), summarizer.Overrode)
	}

	search := findItem(t, res.Tools, "search-hub")
	if !search.Active {
		t.Errorf("search-hub should be enabled at workspace (overriding global disable)")
	}
	if search.Origin != ScopeWorkspace {
		t.Errorf("search-hub.Origin = %v, want workspace", search.Origin)
	}

	// Knowledge layers: global, workspace:workspace, repo:example-service all present.
	labels := map[string]bool{}
	for _, kl := range res.Knowledge {
		labels[kl.Label] = kl.Exists
	}
	for _, want := range []string{"global", "workspace:workspace", "repo:example-service"} {
		exists, ok := labels[want]
		if !ok {
			t.Errorf("missing knowledge layer %q (have %v)", want, labels)
			continue
		}
		if !exists {
			t.Errorf("knowledge layer %q should exist on disk", want)
		}
	}
}

func TestLoadFromDiskCleanHomeRepoAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, "AGENTS.md"), "user-level instructions\n")
	writeFile(t, filepath.Join(home, ".agents", "skills", "lint", "SKILL.md"), "# lint\n")
	writeFile(t, filepath.Join(home, ".agents", "mcp-registry.toml"), `
[servers.docs]
command = "docs"
`)
	repo := filepath.Join(home, "work", "stranger-app")
	writeFile(t, filepath.Join(repo, "AGENTS.md"), "repo-specific instructions\n")
	writeFile(t, filepath.Join(repo, "agents.yaml"), `
skills:
  lint: enabled
tools:
  docs: disabled
`)

	res, err := LoadFromDisk(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Knowledge) != 2 {
		t.Fatalf("knowledge layers = %+v, want user global + repo", res.Knowledge)
	}
	if res.Knowledge[0].Label != "global" || res.Knowledge[1].Label != "repo:stranger-app" {
		t.Fatalf("knowledge labels = %+v, want global then repo:stranger-app", res.Knowledge)
	}
	lint := findItem(t, res.Skills, "lint")
	if !lint.Active || lint.Origin != ScopeRepo {
		t.Errorf("lint = %+v, want active from repo overlay", lint)
	}
	docs := findItem(t, res.Tools, "docs")
	if docs.Active || docs.Origin != ScopeRepo {
		t.Errorf("docs = %+v, want disabled from repo overlay", docs)
	}
}

// TestLoadFromDiskMissingKnowledge: a workspace without AGENTS.md does not create
// a false expected layer. Missing user/repo instructions should be represented as
// absence of knowledge, not drift.
func TestLoadFromDiskMissingKnowledge(t *testing.T) {
	home := fakeHome(t)

	// workspace "ghost" dir exists (so WorkspaceFor returns it) but no AGENTS.md.
	repo := filepath.Join(home, "git", "ghost", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := LoadFromDisk(repo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != "ghost" {
		t.Errorf("Workspace = %q, want ghost", res.Workspace)
	}

	for _, kl := range res.Knowledge {
		if kl.Label == "workspace:ghost" || kl.Label == "repo:app" {
			t.Fatalf("missing knowledge layer should not be synthesized: %+v", res.Knowledge)
		}
	}
}

// TestLoadFromDiskNoCatalogFiles: missing skills dir / registry yields nil catalogs
// (no panic, empty resolution).
func TestLoadFromDiskNoCatalogFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	me := filepath.Join(home, "git", "me")
	if err := os.MkdirAll(me, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := LoadFromDisk(me)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skills) != 0 || len(res.Tools) != 0 {
		t.Errorf("expected empty catalogs, got skills=%v tools=%v", res.Skills, res.Tools)
	}
}

func TestParseActivation(t *testing.T) {
	cases := map[string]Activation{
		"enabled": Enabled, "enable": Enabled, "on": Enabled, "true": Enabled, "yes": Enabled,
		"ENABLED": Enabled, " On ": Enabled,
		"disabled": Disabled, "disable": Disabled, "off": Disabled, "false": Disabled, "no": Disabled,
		"": Unset, "maybe": Unset, "weird": Unset,
	}
	for in, want := range cases {
		if got := parseActivation(in); got != want {
			t.Errorf("parseActivation(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadToolCatalog(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-registry.toml")
	writeFile(t, p, `
[servers.a]
[servers.b]
[other.c]
not a header
[servers.d]
`)
	got := loadToolCatalog(p)
	want := []string{"a", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("loadToolCatalog = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("loadToolCatalog[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// missing file => nil
	if loadToolCatalog(filepath.Join(dir, "nope.toml")) != nil {
		t.Errorf("missing registry should yield nil")
	}
}
