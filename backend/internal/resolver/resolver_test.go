package resolver

import (
	"reflect"
	"testing"
)

// ov is a small helper to build an Overlay terse-ly in tests.
func ov(scope Scope, label string, skills, tools map[string]Activation) Overlay {
	if skills == nil {
		skills = map[string]Activation{}
	}
	if tools == nil {
		tools = map[string]Activation{}
	}
	return Overlay{Scope: scope, Label: label, Skills: skills, Tools: tools}
}

// findItem returns the Item with the given name, or fails the test.
func findItem(t *testing.T, items []Item, name string) Item {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("item %q not found in %v", name, items)
	return Item{}
}

func TestActivationString(t *testing.T) {
	cases := map[Activation]string{Unset: "unset", Enabled: "enabled", Disabled: "disabled"}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Activation(%d).String() = %q, want %q", a, got, want)
		}
	}
}

func TestScopeString(t *testing.T) {
	cases := map[Scope]string{
		ScopeGlobal:    "global",
		ScopeUser:      "user",
		ScopeWorkspace: "workspace",
		ScopeRepo:      "repo",
		Scope(99):      "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Scope(%d).String() = %q, want %q", s, got, want)
		}
	}
}

// TestResolveDefaults: an item no overlay mentions falls to the catalog default,
// with Origin global / label "default" and the appropriate WhyInactive.
func TestResolveDefaults(t *testing.T) {
	cat := Catalog{
		Skills:       []string{"alpha", "beta"},
		Tools:        []string{"gamma"},
		DefaultSkill: Disabled,
		DefaultTool:  Enabled,
	}
	res := Resolve("/home/d/git/me", "/home/d", cat, nil, nil)

	alpha := findItem(t, res.Skills, "alpha")
	if alpha.Active {
		t.Errorf("skill alpha should be inactive by default, got active")
	}
	if alpha.State != Disabled {
		t.Errorf("alpha.State = %v, want Disabled", alpha.State)
	}
	if alpha.OriginLabel != "default" {
		t.Errorf("alpha.OriginLabel = %q, want default", alpha.OriginLabel)
	}
	if alpha.WhyInactive != "not enabled (catalog default)" {
		t.Errorf("alpha.WhyInactive = %q", alpha.WhyInactive)
	}
	if len(alpha.Overrode) != 0 {
		t.Errorf("alpha.Overrode should be empty, got %v", alpha.Overrode)
	}

	gamma := findItem(t, res.Tools, "gamma")
	if !gamma.Active {
		t.Errorf("tool gamma should be active by default (DefaultTool=Enabled)")
	}
	if gamma.WhyInactive != "" {
		t.Errorf("active item should have empty WhyInactive, got %q", gamma.WhyInactive)
	}
}

// TestResolveNarrowestWins: when multiple layers set a value, the narrowest
// (highest scope) wins and broader layers populate Overrode in broadest-first order.
func TestResolveNarrowestWins(t *testing.T) {
	cat := Catalog{
		Skills:       []string{"sk"},
		DefaultSkill: Disabled,
	}
	overlays := []Overlay{
		ov(ScopeGlobal, "global", map[string]Activation{"sk": Enabled}, nil),
		ov(ScopeWorkspace, "workspace:zerg", map[string]Activation{"sk": Disabled}, nil),
		ov(ScopeRepo, "repo:longhouse", map[string]Activation{"sk": Enabled}, nil),
	}
	res := Resolve("/h/git/zerg/longhouse", "/h", cat, overlays, nil)
	sk := findItem(t, res.Skills, "sk")

	if !sk.Active {
		t.Fatalf("sk should be active (repo enabled wins)")
	}
	if sk.Origin != ScopeRepo {
		t.Errorf("sk.Origin = %v, want ScopeRepo", sk.Origin)
	}
	if sk.OriginLabel != "repo:longhouse" {
		t.Errorf("sk.OriginLabel = %q", sk.OriginLabel)
	}
	if sk.State != Enabled {
		t.Errorf("sk.State = %v, want Enabled", sk.State)
	}
	// Overrode should contain the two broader layers, broadest-first.
	wantOverrode := []ScopeState{
		{Scope: ScopeGlobal, Label: "global", State: Enabled},
		{Scope: ScopeWorkspace, Label: "workspace:zerg", State: Disabled},
	}
	if !reflect.DeepEqual(sk.Overrode, wantOverrode) {
		t.Errorf("sk.Overrode = %#v\nwant %#v", sk.Overrode, wantOverrode)
	}
}

// TestResolveDisabledAtNarrowest: a narrower layer disabling an item that broader
// layers enabled => inactive with WhyInactive naming the disabling layer.
func TestResolveDisabledAtNarrowest(t *testing.T) {
	cat := Catalog{Tools: []string{"slack-hub"}, DefaultTool: Enabled}
	overlays := []Overlay{
		ov(ScopeGlobal, "global", nil, map[string]Activation{"slack-hub": Enabled}),
		ov(ScopeRepo, "repo:secret", nil, map[string]Activation{"slack-hub": Disabled}),
	}
	res := Resolve("/h/git/zerg/secret", "/h", cat, overlays, nil)
	it := findItem(t, res.Tools, "slack-hub")
	if it.Active {
		t.Fatalf("slack-hub should be disabled at repo")
	}
	if it.WhyInactive != "disabled at repo:secret" {
		t.Errorf("WhyInactive = %q, want 'disabled at repo:secret'", it.WhyInactive)
	}
	if it.Origin != ScopeRepo {
		t.Errorf("Origin = %v, want repo", it.Origin)
	}
	wantOverrode := []ScopeState{{Scope: ScopeGlobal, Label: "global", State: Enabled}}
	if !reflect.DeepEqual(it.Overrode, wantOverrode) {
		t.Errorf("Overrode = %#v want %#v", it.Overrode, wantOverrode)
	}
}

// TestResolveUnsetLayersSkipped: an overlay that mentions an item as Unset is a
// no-op and must not become the Origin nor appear in Overrode.
func TestResolveUnsetLayersSkipped(t *testing.T) {
	cat := Catalog{Skills: []string{"sk"}, DefaultSkill: Disabled}
	overlays := []Overlay{
		ov(ScopeGlobal, "global", map[string]Activation{"sk": Enabled}, nil),
		ov(ScopeWorkspace, "workspace:w", map[string]Activation{"sk": Unset}, nil),
		ov(ScopeRepo, "repo:r", map[string]Activation{}, nil), // doesn't mention sk
	}
	res := Resolve("/h/git/w/r", "/h", cat, overlays, nil)
	sk := findItem(t, res.Skills, "sk")
	if !sk.Active {
		t.Fatalf("sk should be active from global")
	}
	if sk.Origin != ScopeGlobal {
		t.Errorf("Origin = %v, want global (unset layers skipped)", sk.Origin)
	}
	if len(sk.Overrode) != 0 {
		t.Errorf("Overrode should be empty (only one effective layer), got %v", sk.Overrode)
	}
}

// TestResolveMixedSkillsAndTools verifies skills and tools resolve independently
// and both kinds carry the right Kind tag and are sorted by name.
func TestResolveMixedSkillsAndTools(t *testing.T) {
	cat := Catalog{
		Skills:       []string{"zeta", "alpha"},
		Tools:        []string{"yutool", "btool"},
		DefaultSkill: Disabled,
		DefaultTool:  Disabled,
	}
	overlays := []Overlay{
		ov(ScopeGlobal, "global",
			map[string]Activation{"alpha": Enabled},
			map[string]Activation{"btool": Enabled}),
	}
	res := Resolve("/h/git/me", "/h", cat, overlays, nil)

	// sorted by name
	if res.Skills[0].Name != "alpha" || res.Skills[1].Name != "zeta" {
		t.Errorf("skills not sorted: %v", ActiveNames(res.Skills))
	}
	if res.Tools[0].Name != "btool" || res.Tools[1].Name != "yutool" {
		t.Errorf("tools not sorted: %v", res.Tools)
	}
	for _, it := range res.Skills {
		if it.Kind != "skill" {
			t.Errorf("skill %q has kind %q", it.Name, it.Kind)
		}
	}
	for _, it := range res.Tools {
		if it.Kind != "tool" {
			t.Errorf("tool %q has kind %q", it.Name, it.Kind)
		}
	}
	if findItem(t, res.Skills, "alpha").Active != true {
		t.Errorf("alpha should be active")
	}
	if findItem(t, res.Skills, "zeta").Active != false {
		t.Errorf("zeta should be inactive (default disabled)")
	}
	if findItem(t, res.Tools, "btool").Active != true {
		t.Errorf("btool should be active")
	}
	if findItem(t, res.Tools, "yutool").Active != false {
		t.Errorf("yutool should be inactive (default disabled)")
	}
}

// TestResolveWorkspaceLabelExtraction: Resolve sets res.Workspace from a workspace
// overlay's label (TrimPrefix "workspace:").
func TestResolveWorkspaceFromOverlay(t *testing.T) {
	cat := Catalog{}
	overlays := []Overlay{ov(ScopeWorkspace, "workspace:zerg", nil, nil)}
	res := Resolve("/h/git/zerg/x", "/h", cat, overlays, nil)
	if res.Workspace != "zerg" {
		t.Errorf("res.Workspace = %q, want zerg", res.Workspace)
	}
}

func TestResolveKnowledgePassthrough(t *testing.T) {
	k := []KnowledgeLayer{{Scope: ScopeGlobal, Label: "global", Exists: true}}
	res := Resolve("/p", "/h", Catalog{}, nil, k)
	if !reflect.DeepEqual(res.Knowledge, k) {
		t.Errorf("knowledge not passed through verbatim")
	}
	if res.Path != "/p" || res.Home != "/h" {
		t.Errorf("Path/Home not set: %q %q", res.Path, res.Home)
	}
}

func TestActiveNames(t *testing.T) {
	items := []Item{
		{Name: "on1", Active: true},
		{Name: "off", Active: false},
		{Name: "on2", Active: true},
	}
	got := ActiveNames(items)
	want := []string{"on1", "on2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ActiveNames = %v, want %v", got, want)
	}
	if ActiveNames(nil) != nil {
		t.Errorf("ActiveNames(nil) should be nil")
	}
}

// TestResolveThreeLayerOverrideChainTool: a fuller override chain across all four
// scopes for a tool, asserting the final winner and the complete Overrode list.
func TestResolveFourLayerOverrideChain(t *testing.T) {
	cat := Catalog{Tools: []string{"t"}, DefaultTool: Disabled}
	overlays := []Overlay{
		ov(ScopeGlobal, "global", nil, map[string]Activation{"t": Enabled}),
		ov(ScopeUser, "user", nil, map[string]Activation{"t": Disabled}),
		ov(ScopeWorkspace, "workspace:w", nil, map[string]Activation{"t": Enabled}),
		ov(ScopeRepo, "repo:r", nil, map[string]Activation{"t": Disabled}),
	}
	res := Resolve("/h/git/w/r", "/h", cat, overlays, nil)
	it := findItem(t, res.Tools, "t")
	if it.Active {
		t.Fatalf("t should be disabled at repo")
	}
	if it.Origin != ScopeRepo {
		t.Errorf("Origin = %v, want repo", it.Origin)
	}
	want := []ScopeState{
		{Scope: ScopeGlobal, Label: "global", State: Enabled},
		{Scope: ScopeUser, Label: "user", State: Disabled},
		{Scope: ScopeWorkspace, Label: "workspace:w", State: Enabled},
	}
	if !reflect.DeepEqual(it.Overrode, want) {
		t.Errorf("Overrode = %#v\nwant %#v", it.Overrode, want)
	}
}

func TestWorkspaceFor(t *testing.T) {
	home := "/Users/d"
	cases := []struct {
		name string
		path string
		want string
	}{
		{"repo me", "/Users/d/git/me", "me"},
		{"workspace zerg via longhouse", "/Users/d/git/zerg/longhouse", "zerg"},
		{"deep nesting", "/Users/d/git/zerg/longhouse/internal/x", "zerg"},
		{"exactly git/x", "/Users/d/git/foo", "foo"},
		{"git root itself", "/Users/d/git", ""},
		{"outside git", "/Users/d/Documents/proj", ""},
		{"completely outside home", "/tmp/elsewhere", ""},
		{"trailing slash", "/Users/d/git/zerg/", "zerg"},
		{"dotdot normalized", "/Users/d/git/zerg/longhouse/..", "zerg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WorkspaceFor(c.path, home); got != c.want {
				t.Errorf("WorkspaceFor(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}
