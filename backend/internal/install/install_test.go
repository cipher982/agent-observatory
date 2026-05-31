package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testTarget builds a Target rooted entirely in a temp dir, with launchctl/daemon
// side-effects captured in maps instead of touching the real system.
func testTarget(t *testing.T) (Target, *sideEffects) {
	t.Helper()
	home := t.TempDir()
	se := &sideEffects{setenv: map[string]string{}, loaded: map[string]bool{}}
	tgt := DefaultTarget(home, "/usr/local/bin/agents")
	tgt.LaunchctlSetenv = func(k, v string) error { se.setenv[k] = v; return nil }
	tgt.LaunchctlUnsetenv = func(k string) error { delete(se.setenv, k); return nil }
	tgt.LoadDaemon = func(p string) error { se.loaded[p] = true; return nil }
	tgt.UnloadDaemon = func(p string) error { delete(se.loaded, p); return nil }
	return tgt, se
}

type sideEffects struct {
	setenv map[string]string
	loaded map[string]bool
}

// snapshotTree captures every file path + content under root, for assert-clean.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		out[p] = string(data)
		return nil
	})
	return out
}

// TestInstallThenUninstallIsClean is the core property: after install+uninstall,
// the tree is byte-identical to before (modulo dirs), env unset, daemon unloaded.
func TestInstallThenUninstallIsClean(t *testing.T) {
	tgt, se := testTarget(t)

	// Seed a pre-existing profile to prove we don't clobber the user's content.
	preProfile := "export PATH=/usr/bin\nalias ll='ls -la'\n"
	if err := os.WriteFile(tgt.ProfilePath, []byte(preProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, tgt.Home)

	if err := tgt.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Installed state present.
	st := tgt.Status()
	if !st.ProfileBlock || !st.PlistExists {
		t.Fatalf("post-install status incomplete: %+v", st)
	}
	if se.setenv["NODE_EXTRA_CA_CERTS"] == "" {
		t.Errorf("NODE_EXTRA_CA_CERTS not set via launchctl")
	}
	// Routing is the NE extension's job — the install must NOT set proxy vars or
	// root-replacing trust vars (that was the old global-hijack blast radius).
	for _, banned := range []string{"HTTPS_PROXY", "HTTP_PROXY", "SSL_CERT_FILE", "AWS_CA_BUNDLE"} {
		if _, ok := se.setenv[banned]; ok {
			t.Errorf("install set %s; routing is the extension's job and this breaks unrelated traffic", banned)
		}
	}
	if !se.loaded[tgt.plistPath()] {
		t.Errorf("daemon not loaded")
	}
	// The daemon will create the CA on first run; simulate that so uninstall has
	// something to remove (install only makes the dir).
	_ = os.WriteFile(tgt.caPEMPath(), []byte("FAKE CA PEM"), 0o644)

	if err := tgt.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	// Profile restored EXACTLY.
	got, _ := os.ReadFile(tgt.ProfilePath)
	if string(got) != preProfile {
		t.Errorf("profile not restored:\n got: %q\nwant: %q", got, preProfile)
	}
	// Env unset, daemon unloaded, plist + CA gone.
	if len(se.setenv) != 0 {
		t.Errorf("env not cleared: %v", se.setenv)
	}
	if len(se.loaded) != 0 {
		t.Errorf("daemon still loaded: %v", se.loaded)
	}
	if _, err := os.Stat(tgt.plistPath()); !os.IsNotExist(err) {
		t.Errorf("plist still present")
	}
	if _, err := os.Stat(tgt.CADir); !os.IsNotExist(err) {
		t.Errorf("CA dir still present")
	}

	// Tree equality (only the profile file should exist again, identical).
	after := snapshotTree(t, tgt.Home)
	if after[tgt.ProfilePath] != before[tgt.ProfilePath] {
		t.Errorf("profile content drift after round-trip")
	}
}

// TestIdempotentInstall: installing twice yields exactly ONE managed block and a
// single, parseable env set.
func TestIdempotentInstall(t *testing.T) {
	tgt, _ := testTarget(t)
	os.WriteFile(tgt.ProfilePath, []byte("export PATH=/usr/bin\n"), 0o644)

	for i := 0; i < 3; i++ {
		if err := tgt.Install(); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(tgt.ProfilePath)
	if n := strings.Count(string(data), beginMarker); n != 1 {
		t.Errorf("managed block appears %d times, want 1", n)
	}
	if n := strings.Count(string(data), "export NODE_EXTRA_CA_CERTS="); n != 1 {
		t.Errorf("NODE_EXTRA_CA_CERTS exported %d times, want 1", n)
	}
	// Original content survives exactly once.
	if n := strings.Count(string(data), "export PATH=/usr/bin"); n != 1 {
		t.Errorf("user PATH line count = %d, want 1", n)
	}
}

// TestPartialStateRecovery: uninstall succeeds and cleans up even when only some
// pieces exist (e.g. profile block written but plist missing, or CA missing).
func TestPartialStateRecovery(t *testing.T) {
	// Case A: only profile block exists.
	tgt, _ := testTarget(t)
	os.WriteFile(tgt.ProfilePath, []byte("x=1\n"), 0o644)
	tgt.writeProfileBlock(tgt.caPEMPath()) // only this piece
	if err := tgt.Uninstall(); err != nil {
		t.Fatalf("uninstall partial(profile-only): %v", err)
	}
	got, _ := os.ReadFile(tgt.ProfilePath)
	if string(got) != "x=1\n" {
		t.Errorf("profile not cleanly restored from partial: %q", got)
	}

	// Case B: nothing installed at all — uninstall must be a no-op success.
	tgt2, _ := testTarget(t)
	if err := tgt2.Uninstall(); err != nil {
		t.Errorf("uninstall on clean system should succeed, got %v", err)
	}
}

// TestProfileBlockRoundTripWithMessyContent: the strip/rewrite handles content
// with no trailing newline, content before AND after the block, etc.
func TestProfileBlockRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"export A=1",            // no trailing newline
		"export A=1\n",          // trailing newline
		"line1\nline2\nline3\n", // multiline
	}
	for _, pre := range cases {
		tgt, _ := testTarget(t)
		os.WriteFile(tgt.ProfilePath, []byte(pre), 0o644)
		if err := tgt.Install(); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(tgt.caPEMPath(), []byte("ca"), 0o644)
		if err := tgt.Uninstall(); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(tgt.ProfilePath)
		// Normalize: original content must be present and no managed markers remain.
		if strings.Contains(string(got), beginMarker) || strings.Contains(string(got), endMarker) {
			t.Errorf("markers survived for input %q: %q", pre, got)
		}
		for _, line := range strings.Split(strings.TrimSpace(pre), "\n") {
			if line != "" && !strings.Contains(string(got), line) {
				t.Errorf("original line %q lost for input %q -> %q", line, pre, got)
			}
		}
	}
}

// TestStatusReflectsState exercises Status across the lifecycle.
func TestStatusReflectsState(t *testing.T) {
	tgt, _ := testTarget(t)
	if tgt.Status().Installed {
		t.Error("clean system reports installed")
	}
	tgt.Install()
	os.WriteFile(tgt.caPEMPath(), []byte("ca"), 0o644)
	st := tgt.Status()
	if !st.Installed {
		t.Errorf("post-install not reported installed: %+v", st)
	}
	if st.EnvVars["NODE_EXTRA_CA_CERTS"] == "" {
		t.Errorf("status didn't parse env from profile block")
	}
	tgt.Uninstall()
	if tgt.Status().Installed {
		t.Error("post-uninstall still reports installed")
	}
}
