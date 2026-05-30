package resolver

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFromDisk builds the catalog, knowledge layers, and overlays for a path by
// reading the local agent-context layout, then resolves. It is the production
// entry point used by the CLI and GUI.
//
// Layout assumptions:
//   - Global doctrine: first existing of ~/AGENTS.md, ~/.agents/AGENTS.md,
//     ~/.config/agent-observatory/AGENTS.md, or the legacy ~/git/me/AGENTS.md.
//   - Repo knowledge: AGENTS.md files found while walking from home toward path.
//   - Skill catalog: any <dir>/<name>/SKILL.md under common user skill dirs.
//   - Tool catalog: mcp-registry.toml in common user config dirs.
//   - Activation overlays: agents.yaml (or .agents.yaml) beside any knowledge
//     layer. Missing files are no-ops.
func LoadFromDisk(path string) (Resolution, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Resolution{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	cat := Catalog{
		Skills:       loadSkillCatalogs(skillCatalogDirs(home)),
		Tools:        loadToolCatalogs(toolCatalogPaths(home)),
		DefaultSkill: Disabled, // skills are explicit-on by default
		DefaultTool:  Enabled,  // project default: tools on everywhere unless disabled
	}

	ws := WorkspaceFor(abs, home)

	knowledge := discoverKnowledgeLayers(abs, home)
	overlays := overlaysForKnowledge(knowledge)

	res := Resolve(abs, home, cat, overlays, knowledge)
	res.Workspace = ws
	return res, nil
}

func skillCatalogDirs(home string) []string {
	return []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".config", "agent-observatory", "skills"),
		filepath.Join(home, "git", "me", "skills"), // legacy/private layout; optional
	}
}

func toolCatalogPaths(home string) []string {
	return []string{
		filepath.Join(home, ".config", "agent-observatory", "mcp-registry.toml"),
		filepath.Join(home, ".agents", "mcp-registry.toml"),
		filepath.Join(home, "git", "me", "registry", "mcp-registry.toml"), // legacy/private layout; optional
	}
}

func loadSkillCatalogs(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		for _, name := range loadSkillCatalog(dir) {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func loadToolCatalogs(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		for _, name := range loadToolCatalog(path) {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func discoverKnowledgeLayers(path, home string) []KnowledgeLayer {
	dir := path
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}

	var out []KnowledgeLayer
	seen := map[string]bool{}
	add := func(scope Scope, label, path string) {
		kl := knowledgeLayer(scope, label, path)
		if !kl.Exists || seenKnowledge(seen, path) {
			return
		}
		out = append(out, kl)
	}

	for _, candidate := range globalKnowledgeCandidates(home) {
		kl := knowledgeLayer(ScopeGlobal, "global", candidate)
		if kl.Exists && !seenKnowledge(seen, candidate) {
			out = append(out, kl)
			break
		}
	}

	var dirs []string
	for cur := filepath.Clean(dir); ; cur = filepath.Dir(cur) {
		dirs = append(dirs, cur)
		if cur == filepath.Clean(home) || filepath.Dir(cur) == cur {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if d == filepath.Clean(home) {
			add(ScopeUser, "user", filepath.Join(d, "AGENTS.md"))
			continue
		}
		add(scopeForDir(d, dir, home), labelForDir(d, dir, home), filepath.Join(d, "AGENTS.md"))
	}
	return out
}

func globalKnowledgeCandidates(home string) []string {
	return []string{
		filepath.Join(home, "AGENTS.md"),
		filepath.Join(home, ".agents", "AGENTS.md"),
		filepath.Join(home, ".config", "agent-observatory", "AGENTS.md"),
		filepath.Join(home, "git", "me", "AGENTS.md"), // legacy/private layout; optional
	}
}

func seenKnowledge(seen map[string]bool, path string) bool {
	key := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		key = filepath.Clean(real)
	}
	if seen[key] {
		return true
	}
	seen[key] = true
	return false
}

func overlaysForKnowledge(knowledge []KnowledgeLayer) []Overlay {
	var overlays []Overlay
	for _, kl := range knowledge {
		dir := filepath.Dir(kl.Path)
		ov := loadOverlay(kl.Scope, kl.Label, filepath.Join(dir, "agents.yaml"))
		if len(ov.Skills) == 0 && len(ov.Tools) == 0 {
			ov = loadOverlay(kl.Scope, kl.Label, filepath.Join(dir, ".agents.yaml"))
		}
		overlays = append(overlays, ov)
	}
	return overlays
}

func scopeForDir(dir, target, home string) Scope {
	if dir == target {
		return ScopeRepo
	}
	if filepath.Dir(dir) == filepath.Join(home, "git") {
		return ScopeWorkspace
	}
	return ScopeRepo
}

func labelForDir(dir, target, home string) string {
	base := filepath.Base(dir)
	if dir == target {
		return "repo:" + base
	}
	if filepath.Dir(dir) == filepath.Join(home, "git") {
		return "workspace:" + base
	}
	return "repo:" + base
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
