// Package install performs the ambient, frictionless setup for Agent
// Observatory and its fully reversible teardown.
//
// Routing is handled by the NetworkExtension transparent proxy (see
// app/TransparentProxyExtension): the kernel routes only allowlisted provider
// :443 flows to the local proxy, so there is NO global HTTPS_PROXY/HTTP_PROXY
// hijack and unrelated traffic is never diverted. The install therefore only
// has to deliver two things:
//   - a STABLE CA + an always-on launchd daemon that runs the proxy, and
//   - trust for that CA so agents accept the proxy's leaf certs.
//
// Trust is delivered two ways, matched to how each runtime resolves roots:
//   - login-keychain trust (`agents trust install`, run behind the approved
//     system extension) — honored by rustls/reqwest (Codex) and the AWS Go SDK
//     (Bedrock), which consult the platform trust store; and
//   - NODE_EXTRA_CA_CERTS — an ADDITIVE Node/Bun trust var, because Node does
//     not read the macOS keychain by default. It only ADDS our CA; it never
//     replaces the system roots, so unrelated HTTPS is unaffected.
//
// We deliberately do NOT set HTTPS_PROXY, HTTP_PROXY, SSL_CERT_FILE, or
// AWS_CA_BUNDLE: routing is the extension's job, and SSL_CERT_FILE/AWS_CA_BUNDLE
// would REPLACE the system root bundle and break unrelated traffic.
//
// Everything is parameterized by Target so the QA harness can drive
// install→verify→uninstall→assert-clean in temp roots, never touching the real
// shell. The profile edit is fenced by sentinel markers so uninstall removes
// EXACTLY what install added and nothing else.
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	beginMarker = "# >>> agent-observatory >>>"
	endMarker   = "# <<< agent-observatory <<<"
	labelPrefix = "com.github.cipher982.agentobservatory"
)

// EnvVars are the variables the install sets globally. Both are ADDITIVE — they
// add our CA without replacing the system roots, so unrelated HTTPS is
// unaffected — and both are needed because the two flagship runtimes don't read
// the macOS keychain reliably:
//   - NODE_EXTRA_CA_CERTS  — Claude Code (Node/Bun) adds our CA to Node's roots.
//   - CODEX_CA_CERTIFICATE — Codex CLI (rustls/native, NOT rustls-platform-verifier)
//     adds our CA via its own custom-CA path; keychain trust alone is unreliable
//     for it (especially its WebSocket path). Codex honors this and SSL_CERT_FILE;
//     we use CODEX_CA_CERTIFICATE because it's additive, not root-replacing.
// Routing is the NetworkExtension's job, so no proxy vars are set; the AWS Go SDK
// (Bedrock) reads the login keychain and needs no env.
var EnvVars = []string{"NODE_EXTRA_CA_CERTS", "CODEX_CA_CERTIFICATE"}

// Target captures every path/knob the installer touches, so it can run against a
// fake root in tests or the real system in production. Construct via DefaultTarget
// or build by hand in tests.
type Target struct {
	Home        string // root for ~/.zshenv, state, CA
	ProfilePath string // shell profile to edit (e.g. <home>/.zshenv)
	StateDir    string // where CA + runtime state live
	CADir       string // where the stable CA is written
	LaunchDir   string // LaunchAgents dir for the plist
	BinPath     string // absolute path to the `agents` binary the daemon runs
	ProxyAddr   string // host:port the proxy listens on (e.g. 127.0.0.1:7879)
	APIPort     int    // monitor API/stream port

	// Hooks let tests stub side effects that can't run in a sandbox.
	// When nil, the real implementation is used (no-op stubs for launchctl in tests).
	LaunchctlSetenv   func(key, val string) error // nil => skip (tests)
	LaunchctlUnsetenv func(key string) error
	LoadDaemon        func(plistPath string) error // nil => skip (tests)
	UnloadDaemon      func(plistPath string) error
}

// DefaultTarget builds a Target for the real machine.
func DefaultTarget(home, binPath string) Target {
	state := filepath.Join(home, ".local", "state", "agent-observatory")
	return Target{
		Home:        home,
		ProfilePath: filepath.Join(home, ".zshenv"),
		StateDir:    state,
		CADir:       filepath.Join(state, "ca"),
		LaunchDir:   filepath.Join(home, "Library", "LaunchAgents"),
		BinPath:     binPath,
		ProxyAddr:   "127.0.0.1:7879",
		APIPort:     7878,
	}
}

func (t Target) caPEMPath() string { return filepath.Join(t.CADir, "observatory-ca.pem") }
func (t Target) plistPath() string { return filepath.Join(t.LaunchDir, labelPrefix+".plist") }
func (t Target) label() string     { return labelPrefix }

// Public path accessors (for CLI output).
func (t Target) CAPEMPublic() string     { return t.caPEMPath() }
func (t Target) PlistPathPublic() string { return t.plistPath() }

// Status reports what's currently installed (for `agents status` + tests).
type Status struct {
	CAExists     bool
	ProfileBlock bool
	PlistExists  bool
	EnvVars      map[string]string // var -> value found in the profile block
	Installed    bool              // all three present
}

// Status inspects the target without changing anything.
func (t Target) Status() Status {
	s := Status{EnvVars: map[string]string{}}
	if _, err := os.Stat(t.caPEMPath()); err == nil {
		s.CAExists = true
	}
	if _, err := os.Stat(t.plistPath()); err == nil {
		s.PlistExists = true
	}
	if data, err := os.ReadFile(t.ProfilePath); err == nil {
		if block, ok := extractBlock(string(data)); ok {
			s.ProfileBlock = true
			for _, line := range strings.Split(block, "\n") {
				if k, v, ok := parseExport(line); ok {
					s.EnvVars[k] = v
				}
			}
		}
	}
	s.Installed = s.CAExists && s.ProfileBlock && s.PlistExists
	return s
}

// Install performs the full ambient setup. Idempotent: re-running replaces the
// managed profile block + plist in place and reuses the existing CA.
func (t Target) Install() error {
	// 1) Stable CA (reused if present — see wire.LoadOrCreateCA, invoked by the
	//    daemon; here we just ensure the dir exists and remember the path).
	if err := os.MkdirAll(t.CADir, 0o755); err != nil {
		return fmt.Errorf("ca dir: %w", err)
	}
	if err := os.MkdirAll(t.StateDir, 0o755); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	// The CA itself is created by the daemon (wire.LoadOrCreateCA) on first run;
	// but env vars must point at its eventual path, which is deterministic.
	caPath := t.caPEMPath()

	// 2) Profile block (idempotent replace).
	if err := t.writeProfileBlock(caPath); err != nil {
		return fmt.Errorf("profile: %w", err)
	}

	// 3) launchctl setenv so GUI-launched apps inherit too (best-effort/stubbed).
	env := t.envMap(caPath)
	if t.LaunchctlSetenv != nil {
		for _, k := range EnvVars {
			if err := t.LaunchctlSetenv(k, env[k]); err != nil {
				return fmt.Errorf("launchctl setenv %s: %w", k, err)
			}
		}
	}

	// 4) launchd plist + load.
	if err := t.writePlist(); err != nil {
		return fmt.Errorf("plist: %w", err)
	}
	if t.LoadDaemon != nil {
		if err := t.LoadDaemon(t.plistPath()); err != nil {
			return fmt.Errorf("load daemon: %w", err)
		}
	}
	return nil
}

// Uninstall reverses Install completely: removes the profile block, unsets env,
// unloads + deletes the plist, and removes the CA/state. Safe to run when only
// partially installed (each step tolerates absence) — this is the partial-state
// recovery path.
func (t Target) Uninstall() error {
	var errs []string

	// 1) profile block removal (leaves the rest of the file untouched).
	if err := t.removeProfileBlock(); err != nil {
		errs = append(errs, "profile: "+err.Error())
	}
	// 2) launchctl unsetenv.
	if t.LaunchctlUnsetenv != nil {
		for _, k := range EnvVars {
			_ = t.LaunchctlUnsetenv(k) // best-effort
		}
	}
	// 3) unload + remove plist.
	if t.UnloadDaemon != nil {
		_ = t.UnloadDaemon(t.plistPath())
	}
	if err := removeIfExists(t.plistPath()); err != nil {
		errs = append(errs, "plist: "+err.Error())
	}
	// 4) remove CA + all runtime state (CA, daemon.log, persisted wire-*.json).
	// CADir lives under StateDir, so removing StateDir reverses install fully.
	if err := os.RemoveAll(t.StateDir); err != nil {
		errs = append(errs, "state: "+err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("uninstall issues: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (t Target) envMap(caPath string) map[string]string {
	return map[string]string{
		"NODE_EXTRA_CA_CERTS":  caPath,
		"CODEX_CA_CERTIFICATE": caPath,
	}
}

// writeProfileBlock writes the fenced managed block, replacing any prior one.
func (t Target) writeProfileBlock(caPath string) error {
	existing := ""
	if data, err := os.ReadFile(t.ProfilePath); err == nil {
		existing = stripBlock(string(data))
	}
	env := t.envMap(caPath)
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(beginMarker + "\n")
	b.WriteString("# Managed by agent-observatory. Do not edit; run `agents uninstall` to remove.\n")
	for _, k := range EnvVars {
		b.WriteString(fmt.Sprintf("export %s=%q\n", k, env[k]))
	}
	b.WriteString(endMarker + "\n")
	if err := os.MkdirAll(filepath.Dir(t.ProfilePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(t.ProfilePath, []byte(b.String()), 0o644)
}

func (t Target) removeProfileBlock() error {
	data, err := os.ReadFile(t.ProfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cleaned := stripBlock(string(data))
	return os.WriteFile(t.ProfilePath, []byte(cleaned), 0o644)
}

// --- block helpers ---

func extractBlock(content string) (string, bool) {
	bi := strings.Index(content, beginMarker)
	ei := strings.Index(content, endMarker)
	if bi < 0 || ei < 0 || ei < bi {
		return "", false
	}
	return content[bi+len(beginMarker) : ei], true
}

// stripBlock removes the managed block (and its surrounding newlines) cleanly.
func stripBlock(content string) string {
	bi := strings.Index(content, beginMarker)
	ei := strings.Index(content, endMarker)
	if bi < 0 || ei < 0 || ei < bi {
		return content
	}
	before := content[:bi]
	after := content[ei+len(endMarker):]
	before = strings.TrimRight(before, "\n")
	after = strings.TrimLeft(after, "\n")
	switch {
	case before == "" && after == "":
		return ""
	case before == "":
		return after + "\n"
	case after == "":
		return before + "\n"
	default:
		return before + "\n" + after + "\n"
	}
}

func parseExport(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "export ") {
		return "", "", false
	}
	rest := strings.TrimPrefix(line, "export ")
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return "", "", false
	}
	key := rest[:eq]
	val := strings.Trim(rest[eq+1:], `"`)
	return key, val, true
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
