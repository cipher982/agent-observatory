package resolver

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFromDisk builds the catalog, knowledge layers, and overlays for a path by
// reading the local agent-context layout, then resolves. It is the production
// entry point used by the CLI and GUI.
//
// Layout assumptions (v1):
//   - Global doctrine:   <home>/git/me/AGENTS.md
//   - Skill catalog:     <home>/git/me/skills/<name>/SKILL.md
//   - Tool catalog:      <home>/git/me/registry/mcp-registry.toml ([servers.<name>])
//   - Activation overlays (optional, any layer): an "agents.yaml" next to the
//     AGENTS.md for that scope. Global overlay: <home>/git/me/agents.yaml.
//     Workspace overlay: <home>/git/<workspace>/agents.yaml.
//     Repo overlay: <path>/agents.yaml (or .agents.yaml).
//   - Workspace knowledge: <home>/git/<workspace>/AGENTS.md
//   - Repo knowledge:      <path>/AGENTS.md
func LoadFromDisk(path string) (Resolution, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Resolution{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	me := filepath.Join(home, "git", "me")

	cat := Catalog{
		Skills:       loadSkillCatalog(filepath.Join(me, "skills")),
		Tools:        loadToolCatalog(filepath.Join(me, "registry", "mcp-registry.toml")),
		DefaultSkill: Disabled, // skills are explicit-on by default
		DefaultTool:  Enabled,  // project default: tools on everywhere unless disabled
	}

	ws := WorkspaceFor(abs, home)

	// Build knowledge layers broadest-first.
	var knowledge []KnowledgeLayer
	knowledge = append(knowledge, knowledgeLayer(ScopeGlobal, "global", filepath.Join(me, "AGENTS.md")))
	// user-level (~/AGENTS.md is typically a symlink to global; include if present and distinct)
	if userAgents := filepath.Join(home, "AGENTS.md"); userAgents != filepath.Join(me, "AGENTS.md") {
		if kl := knowledgeLayer(ScopeUser, "user", userAgents); kl.Exists {
			// only add if it resolves to something other than the global file
			if real, _ := filepath.EvalSymlinks(userAgents); real != filepath.Join(me, "AGENTS.md") {
				knowledge = append(knowledge, kl)
			}
		}
	}
	if ws != "" && ws != "me" {
		knowledge = append(knowledge, knowledgeLayer(ScopeWorkspace, "workspace:"+ws, filepath.Join(home, "git", ws, "AGENTS.md")))
	}
	// repo layer: the AGENTS.md at the path itself, if it's deeper than the workspace root
	repoAgents := filepath.Join(abs, "AGENTS.md")
	wsRoot := filepath.Join(home, "git", ws)
	if abs != wsRoot && abs != me {
		knowledge = append(knowledge, knowledgeLayer(ScopeRepo, "repo:"+filepath.Base(abs), repoAgents))
	}

	// Build overlays broadest-first.
	var overlays []Overlay
	overlays = append(overlays, loadOverlay(ScopeGlobal, "global", filepath.Join(me, "agents.yaml")))
	if ws != "" && ws != "me" {
		overlays = append(overlays, loadOverlay(ScopeWorkspace, "workspace:"+ws, filepath.Join(home, "git", ws, "agents.yaml")))
	}
	if abs != wsRoot && abs != me {
		ov := loadOverlay(ScopeRepo, "repo:"+filepath.Base(abs), filepath.Join(abs, "agents.yaml"))
		if len(ov.Skills) == 0 && len(ov.Tools) == 0 {
			ov = loadOverlay(ScopeRepo, "repo:"+filepath.Base(abs), filepath.Join(abs, ".agents.yaml"))
		}
		overlays = append(overlays, ov)
	}

	res := Resolve(abs, home, cat, overlays, knowledge)
	res.Workspace = ws
	return res, nil
}

// loadSkillCatalog lists skill names from <dir>/<name>/SKILL.md.
func loadSkillCatalog(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// loadToolCatalog extracts MCP server names from [servers.<name>] headers in the
// registry TOML. A tiny line scanner avoids a TOML dependency for v1.
func loadToolCatalog(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[servers.") && strings.HasSuffix(line, "]") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "[servers."), "]")
			out = append(out, name)
		}
	}
	return out
}

// overlayFile is the on-disk shape of an agents.yaml.
type overlayFile struct {
	Skills           map[string]string `yaml:"skills"`
	Tools            map[string]string `yaml:"tools"`
	Classification   string            `yaml:"classification"`
	AllowedDevices   []string          `yaml:"allowed_devices"`
	ForbiddenDevices []string          `yaml:"forbidden_devices"`
}

// loadOverlay reads an agents.yaml at path. A missing file yields an empty
// overlay (all Unset), which is the correct no-op.
func loadOverlay(scope Scope, label, path string) Overlay {
	ov := Overlay{Scope: scope, Label: label, Skills: map[string]Activation{}, Tools: map[string]Activation{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return ov
	}
	var of overlayFile
	if yaml.Unmarshal(data, &of) != nil {
		return ov
	}
	for k, v := range of.Skills {
		ov.Skills[k] = parseActivation(v)
	}
	for k, v := range of.Tools {
		ov.Tools[k] = parseActivation(v)
	}
	ov.Classification = of.Classification
	ov.AllowedDevices = of.AllowedDevices
	ov.ForbiddenDevices = of.ForbiddenDevices
	return ov
}

func parseActivation(s string) Activation {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "enabled", "enable", "on", "true", "yes":
		return Enabled
	case "disabled", "disable", "off", "false", "no":
		return Disabled
	default:
		return Unset
	}
}
