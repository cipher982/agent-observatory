package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/procenv"
)

// runDoctor implements `agents doctor wire`: an honest per-runtime capability
// report for the VERIFIED wire tier. It tells the user exactly which runtimes
// can be wire-verified and how (no fake promises).
func runDoctor(args []string) int {
	topic := "wire"
	if len(args) > 0 {
		topic = args[0]
	}
	if topic != "wire" {
		fmt.Println("usage: agents doctor wire")
		return 2
	}

	fmt.Println("agents doctor wire — VERIFIED tier capability per runtime")
	fmt.Println()

	type probe struct {
		runtime string
		bin     string
		stack   string
		state   string
		mechad  string // how interception is achieved
		envKey  string
	}
	probes := []probe{
		{"claude", "claude", "Bun/Node + @aws-sdk (Bedrock)", "supported", "transparent provider flows are full-captured only when the source is Claude Code and NODE_EXTRA_CA_CERTS is current", "NODE_EXTRA_CA_CERTS"},
		{"codex", "codex", "Node shim → signed native Codex binary", "supported", "transparent provider flows are full-captured only when the source is Codex and CODEX_CA_CERTIFICATE is current; /responses WS still uses 426→HTTP fallback", "CODEX_CA_CERTIFICATE"},
		{"gemini", "gemini", "Node CLI", "candidate", "transparent provider flows are full-captured only when the source is attributable to Gemini CLI and NODE_EXTRA_CA_CERTS is current", "NODE_EXTRA_CA_CERTS"},
		{"hatch", "opencode", "Hatch MCP → persistent OpenCode server/run (Bun + AI SDK)", "diagnostic", "persistent OpenCode servers are reported, but 0.3 should not require Hatch/OpenCode patching for safety", ""},
		{"antigravity", "antigravity", "Node (opaque .pb transcripts)", "metadata-only", "not a 0.3 full-capture target; provider flows should remain safe if unsupported", "NODE_EXTRA_CA_CERTS"},
	}

	for _, p := range probes {
		path, err := exec.LookPath(p.bin)
		installed := err == nil
		ver := ""
		if installed {
			ver = cliVersion(p.bin)
		}
		status := green("ready")
		note := p.mechad
		switch {
		case !installed:
			status = gray("not installed")
			note = "binary not on PATH"
		case p.runtime == "hatch":
			if !hatchOpenCodeTrustOK() {
				status = yellow("restart needed")
			}
		case p.state == "candidate" || p.state == "metadata-only":
			status = yellow(p.state)
		case p.runtime == "antigravity":
			status = yellow("unverified")
		}
		fmt.Printf("  %-12s %s\n", p.runtime, status)
		if installed {
			fmt.Printf("      bin:   %s%s\n", path, verSuffix(ver))
		}
		fmt.Printf("      state: %s\n", p.state)
		fmt.Printf("      stack: %s\n", p.stack)
		fmt.Printf("      wire:  %s\n", note)
		if installed {
			for _, diag := range runtimeTrustDiagnostics(p.runtime, p.envKey) {
				fmt.Printf("      diag:  %s\n", diag)
			}
		}
		if p.runtime == "hatch" {
			for _, diag := range hatchOpenCodeDiagnostics() {
				fmt.Printf("      diag:  %s\n", diag)
			}
		}
		fmt.Println()
	}

	fmt.Println("Routing is handled by the NetworkExtension transparent proxy: the kernel")
	fmt.Println("sends only allowlisted provider :443 flows to Observatory, so unrelated")
	fmt.Println("traffic is never diverted (no global HTTPS_PROXY hijack).")
	fmt.Println("Full VERIFIED capture uses a local MITM hop, but 0.3 gates that hop by")
	fmt.Println("source runtime and current additive trust. Unknown, stale, or unsupported")
	fmt.Println("provider-bound clients are tunneled opaquely so custom tools keep working.")
	fmt.Println("Trust lives in your LOGIN keychain (never the System keychain), plus additive")
	fmt.Println("per-runtime CA vars: NODE_EXTRA_CA_CERTS and CODEX_CA_CERTIFICATE.")
	fmt.Println("Provider-bound upstream TLS still uses system trust.")
	return 0
}

func hatchOpenCodeTrustOK() bool {
	for _, d := range hatchOpenCodeDiagnostics() {
		if strings.Contains(d, "missing") || strings.Contains(d, "stale") {
			return false
		}
	}
	return true
}

func hatchOpenCodeDiagnostics() []string {
	expected := observatoryCAPath()
	if expected == "" {
		return []string{"Observatory CA path is unknown; run `agents install` before enabling capture."}
	}
	out, err := exec.Command("pgrep", "-f", "opencode serve").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return []string{"no running Hatch OpenCode server found; the next Hatch call should inherit current trust."}
	}

	var diags []string
	for _, pid := range strings.Fields(string(out)) {
		cmdlineBytes, err := exec.Command("ps", "eww", "-p", pid, "-o", "command=").Output()
		if err != nil {
			continue
		}
		cmdline := string(cmdlineBytes)
		if !strings.Contains(cmdline, "hatch/mcp/opencode") && !strings.Contains(cmdline, "hatch/mcp-runtime") {
			continue
		}
		info, _ := procenv.Lookup(mustAtoi(pid))
		got := info.Env["NODE_EXTRA_CA_CERTS"]
		switch {
		case got == "":
			diags = append(diags, fmt.Sprintf("opencode serve pid %s is missing NODE_EXTRA_CA_CERTS; restart Hatch MCP before enabling capture.", pid))
		case got != expected:
			diags = append(diags, fmt.Sprintf("opencode serve pid %s has stale NODE_EXTRA_CA_CERTS; restart Hatch MCP before enabling capture.", pid))
		default:
			diags = append(diags, fmt.Sprintf("opencode serve pid %s has current Observatory trust.", pid))
		}
	}
	if len(diags) == 0 {
		return []string{"no running Hatch-managed OpenCode server found; the next Hatch call should inherit current trust."}
	}
	return diags
}

func runtimeTrustDiagnostics(runtime, envKey string) []string {
	if envKey == "" {
		return nil
	}
	expected := observatoryCAPath()
	if expected == "" {
		return []string{"Observatory CA path is unknown; run `agents install` before enabling capture."}
	}
	out, err := exec.Command("ps", "eww", "-axo", "pid=,command=").Output()
	if err != nil {
		return []string{"could not inspect running processes for trust state."}
	}
	var diags []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !runtimeCommandMatches(runtime, line) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid := fields[0]
		info, _ := procenv.Lookup(mustAtoi(pid))
		got := info.Env[envKey]
		switch {
		case got == "":
			diags = append(diags, fmt.Sprintf("running pid %s is missing %s; 0.3 policy will tunnel it instead of full-capturing.", pid, envKey))
		case got != expected:
			diags = append(diags, fmt.Sprintf("running pid %s has stale %s; 0.3 policy will tunnel it instead of full-capturing.", pid, envKey))
		default:
			diags = append(diags, fmt.Sprintf("running pid %s has current Observatory trust.", pid))
		}
	}
	if len(diags) == 0 {
		return []string{"no running process found; next fresh launch should inherit current trust."}
	}
	return diags
}

func runtimeCommandMatches(runtime, line string) bool {
	lower := strings.ToLower(line)
	exe := firstCommandToken(lower)
	switch runtime {
	case "claude":
		if strings.Contains(exe, "/applications/claude.app/") {
			return false
		}
		return exe == "claude" || strings.HasSuffix(exe, "/claude")
	case "codex":
		if exe == "./codex" || strings.Contains(exe, "/applications/codex.app/") {
			return false
		}
		return exe == "codex" || strings.HasSuffix(exe, "/codex") || strings.Contains(exe, "/codex-darwin-")
	case "gemini":
		return (exe == "node" || strings.HasSuffix(exe, "/node")) && strings.Contains(lower, "gemini")
	case "antigravity":
		return exe == "antigravity" || strings.HasSuffix(exe, "/antigravity")
	default:
		return false
	}
}

func firstCommandToken(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

func observatoryCAPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "agent-observatory", "ca", "observatory-ca.pem")
}

func mustAtoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func cliVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

func verSuffix(v string) string {
	if v == "" {
		return ""
	}
	return "  (" + v + ")"
}

func yellow(s string) string { return "\033[0;33m" + s + "\033[0m" }
