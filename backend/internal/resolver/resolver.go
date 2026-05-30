// Package resolver computes the effective agent context for a given filesystem
// path: which knowledge (AGENTS.md) layers apply, and which skills and tools are
// active — each annotated with the scope layer it came from (its provenance).
//
// Two orthogonal mechanisms (see the design doc, section 5):
//
//   - Scope: an ordered precedence stack, narrowest wins.
//     global < user < workspace < repo  (local override reserved for later)
//     Knowledge layers COMPOSE down the stack (repo adds to global).
//     Discrete items (skill, tool) take the value of the narrowest layer
//     that mentions them.
//
//   - Activation: a filter set at any layer. Each item is, per layer,
//     enabled | disabled | unset. The narrowest non-"unset" layer wins.
//     An item never mentioned anywhere falls to the catalog default.
//
// The non-negotiable feature: every resolved item carries its Origin layer and,
// when it shadows a broader layer, what it overrode. The dashboard never says
// merely "active" — it says "active (from repo, overrides global)".
//
// v1 is read-only and filesystem-backed. It reserves classification fields
// (Classification/AllowedDevices/ForbiddenDevices) in the activation overlay
// type but does not yet enforce them.
package resolver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope identifies a precedence layer. Lower Rank = broader (global is broadest).
type Scope int

const (
	ScopeGlobal Scope = iota // ~/git/me (David's canonical doctrine)
	ScopeUser                // ~ (user-level; often == global for a solo operator)
	ScopeWorkspace           // a named grouping, e.g. ~/git/zerg
	ScopeRepo                // the specific repo / cwd
)

func (s Scope) String() string {
	switch s {
	case ScopeGlobal:
		return "global"
	case ScopeUser:
		return "user"
	case ScopeWorkspace:
		return "workspace"
	case ScopeRepo:
		return "repo"
	default:
		return "unknown"
	}
}

// Activation is the tri-state an overlay can assign to an item at a layer.
type Activation int

const (
	Unset Activation = iota
	Enabled
	Disabled
)

func (a Activation) String() string {
	switch a {
	case Enabled:
		return "enabled"
	case Disabled:
		return "disabled"
	default:
		return "unset"
	}
}

// KnowledgeLayer is one AGENTS.md (or equivalent) file that applies, in
// precedence order (broadest first).
type KnowledgeLayer struct {
	Scope  Scope
	Label  string // human label, e.g. "global", "workspace:zerg", "repo:longhouse"
	Path   string // the AGENTS.md file on disk
	Exists bool   // false => the layer was expected here but the file is missing
	Bytes  int    // size of the file, 0 if missing
}

// Item is a resolved skill or tool with full provenance.
type Item struct {
	Name        string
	Kind        string // "skill" | "tool"
	Active      bool
	Origin      Scope      // the layer that decided the effective activation
	OriginLabel string     // human label of that layer
	State       Activation // the winning activation value
	// Overrode records broader layers this item shadowed (for "overrides global").
	Overrode []ScopeState
	// WhyInactive is a short reason when Active is false (e.g. "disabled at workspace").
	WhyInactive string
}

// ScopeState is a (layer, value) pair used to show override chains.
type ScopeState struct {
	Scope Scope
	Label string
	State Activation
}

// Resolution is the full effective context for a path.
type Resolution struct {
	Path      string
	Home      string
	Workspace string // workspace name if the path is inside a known workspace, else ""
	Knowledge []KnowledgeLayer
	Skills    []Item
	Tools     []Item
}

// Overlay is the per-layer activation config (an agents.yaml). All maps are
// name -> Activation. Reserved classification fields are parsed but not enforced
// in v1.
type Overlay struct {
	Scope            Scope
	Label            string
	Skills           map[string]Activation
	Tools            map[string]Activation
	Classification   string   // reserved
	AllowedDevices   []string // reserved
	ForbiddenDevices []string // reserved
}

// Catalog is the universe of known skills and tools plus their default
// activation when no layer mentions them.
type Catalog struct {
	Skills        []string
	Tools         []string
	DefaultSkill  Activation // default for a skill no overlay mentions
	DefaultTool   Activation // default for a tool no overlay mentions
}

// Resolve computes the effective context for path using the supplied catalog and
// the ordered overlays (broadest first). Callers usually get these via
// LoadFromDisk, but the core logic is pure for testability.
func Resolve(path, home string, cat Catalog, overlays []Overlay, knowledge []KnowledgeLayer) Resolution {
	res := Resolution{
		Path:      path,
		Home:      home,
		Knowledge: knowledge,
	}
	// Workspace name, if discoverable from the overlays.
	for _, o := range overlays {
		if o.Scope == ScopeWorkspace {
			res.Workspace = strings.TrimPrefix(o.Label, "workspace:")
		}
	}

	res.Skills = resolveItems("skill", cat.Skills, cat.DefaultSkill, overlays, func(o Overlay) map[string]Activation { return o.Skills })
	res.Tools = resolveItems("tool", cat.Tools, cat.DefaultTool, overlays, func(o Overlay) map[string]Activation { return o.Tools })
	return res
}

// resolveItems applies narrowest-non-unset-wins across overlays for one kind.
func resolveItems(kind string, names []string, def Activation, overlays []Overlay, pick func(Overlay) map[string]Activation) []Item {
	out := make([]Item, 0, len(names))
	for _, name := range names {
		it := Item{Name: name, Kind: kind, State: def, Origin: ScopeGlobal, OriginLabel: "default"}
		mentioned := false
		// overlays are broadest-first; walk in order, remembering the narrowest
		// non-unset as the winner, and record every layer that set a value so we
		// can show the override chain.
		for _, o := range overlays {
			v := pick(o)[name]
			if v == Unset {
				continue
			}
			if mentioned {
				// previous winner becomes something this narrower layer overrode
				it.Overrode = append(it.Overrode, ScopeState{Scope: it.Origin, Label: it.OriginLabel, State: it.State})
			}
			it.State = v
			it.Origin = o.Scope
			it.OriginLabel = o.Label
			mentioned = true
		}
		if !mentioned {
			it.OriginLabel = "default"
		}
		it.Active = it.State == Enabled
		if !it.Active {
			if mentioned {
				it.WhyInactive = "disabled at " + it.OriginLabel
			} else {
				it.WhyInactive = "not enabled (catalog default)"
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WorkspaceFor returns the workspace name for a path using the v1 path-based
// rule: the first segment under <home>/git that is not the path's own repo tail.
// e.g. ~/git/zerg/longhouse -> "zerg"; ~/git/me -> "me".
// Returns "" when the path is not under <home>/git.
func WorkspaceFor(path, home string) string {
	clean := filepath.Clean(path)
	gitRoot := filepath.Join(home, "git")
	rel, err := filepath.Rel(gitRoot, clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	if rel == "." {
		return ""
	}
	segs := strings.Split(rel, string(filepath.Separator))
	return segs[0]
}

// ActiveNames returns the names of items whose Active is true.
func ActiveNames(items []Item) []string {
	var out []string
	for _, it := range items {
		if it.Active {
			out = append(out, it.Name)
		}
	}
	return out
}

// fileInfo is a tiny helper for knowledge layers.
func knowledgeLayer(scope Scope, label, path string) KnowledgeLayer {
	kl := KnowledgeLayer{Scope: scope, Label: label, Path: path}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		kl.Exists = true
		kl.Bytes = int(fi.Size())
	}
	return kl
}
