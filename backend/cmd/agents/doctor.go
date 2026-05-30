package main

import (
	"fmt"
	"os/exec"
	"strings"
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
		mechad  string // how interception is achieved
	}
	probes := []probe{
		{"claude", "claude", "Bun/Node + @aws-sdk (Bedrock)", "HTTPS_PROXY + NODE_EXTRA_CA_CERTS; forwards SigV4 byte-identical"},
		{"codex", "codex", "Rust/rustls (reqwest)", "HTTPS_PROXY + SSL_CERT_FILE"},
		{"antigravity", "antigravity", "Node (opaque .pb transcripts)", "HTTPS_PROXY + NODE_EXTRA_CA_CERTS (untested)"},
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
		case p.runtime == "antigravity":
			status = yellow("unverified")
		}
		fmt.Printf("  %-12s %s\n", p.runtime, status)
		if installed {
			fmt.Printf("      bin:   %s%s\n", path, verSuffix(ver))
		}
		fmt.Printf("      stack: %s\n", p.stack)
		fmt.Printf("      wire:  %s\n", note)
		fmt.Println()
	}

	fmt.Println("After `agents install`, newly launched agents route through Observatory automatically.")
	fmt.Println("Verified capture uses a local MITM hop between the agent process and Observatory.")
	fmt.Println("Trust is injected through NODE_EXTRA_CA_CERTS / SSL_CERT_FILE / AWS_CA_BUNDLE,")
	fmt.Println("never by installing Observatory's CA into the macOS System keychain.")
	fmt.Println("Provider-bound upstream TLS still uses the normal system trust store.")
	return 0
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
