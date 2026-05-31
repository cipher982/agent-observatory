package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/cipher982/agent-observatory/backend/internal/install"
)

// realTarget builds the production install Target with real launchctl/launchd
// side effects wired in.
func realTarget() (install.Target, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Target{}, err
	}
	// The daemon runs THIS binary; resolve its absolute path.
	self, err := os.Executable()
	if err != nil {
		return install.Target{}, err
	}
	t := install.DefaultTarget(home, self)
	t.LaunchctlSetenv = func(k, v string) error {
		return exec.Command("launchctl", "setenv", k, v).Run()
	}
	t.LaunchctlUnsetenv = func(k string) error {
		return exec.Command("launchctl", "unsetenv", k).Run()
	}
	t.LoadDaemon = func(plist string) error {
		// bootout any prior, then bootstrap fresh (idempotent).
		uid := fmt.Sprintf("gui/%d", os.Getuid())
		_ = exec.Command("launchctl", "bootout", uid, plist).Run()
		return exec.Command("launchctl", "bootstrap", uid, plist).Run()
	}
	t.UnloadDaemon = func(plist string) error {
		uid := fmt.Sprintf("gui/%d", os.Getuid())
		return exec.Command("launchctl", "bootout", uid, plist).Run()
	}
	return t, nil
}

func runInstall(args []string) int {
	t, err := realTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		return 1
	}
	if err := t.Install(); err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}
	fmt.Println("Agent Observatory installed.")
	fmt.Println()
	fmt.Println("  local proxy daemon:", t.PlistPathPublic())
	fmt.Println("  local CA:", t.CAPEMPublic())
	fmt.Println("  Node trust (additive):", t.ProfilePath, "+ launchctl setenv (NODE_EXTRA_CA_CERTS)")
	fmt.Println()
	fmt.Println("Enable the NetworkExtension in Agent Observatory to route provider flows here;")
	fmt.Println("it diverts only allowlisted LLM hosts, so unrelated traffic is untouched.")
	fmt.Println("Trust the local CA in your login keychain with:  agents trust install")
	fmt.Println("Newly launched agents are then captured; already-running shells need a restart.")
	fmt.Println("Remove everything with:  agents uninstall")
	return 0
}

func runUninstall(args []string) int {
	t, err := realTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		return 1
	}
	// Remove keychain CA trust (NE path) BEFORE deleting the CA file, since the
	// removal references the cert on disk. No-op/non-fatal if it was never trusted.
	_ = trustRemove(t.CAPEMPublic())
	if err := t.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall completed with issues: %v\n", err)
		return 1
	}
	fmt.Println("Agent Observatory uninstalled. System state restored.")
	fmt.Println("Open a new terminal for the env changes to clear.")
	return 0
}

func runStatus(args []string) int {
	t, err := realTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	s := t.Status()
	mark := func(b bool) string {
		if b {
			return green("✓")
		}
		return red("✗")
	}
	fmt.Println("Agent Observatory — install status")
	fmt.Printf("  %s stable CA         %s\n", mark(s.CAExists), t.CAPEMPublic())
	fmt.Printf("  %s Node trust block  %s\n", mark(s.ProfileBlock), t.ProfilePath)
	fmt.Printf("  %s launchd daemon    %s\n", mark(s.PlistExists), t.PlistPathPublic())
	if s.ProfileBlock {
		for _, k := range install.EnvVars {
			if v := s.EnvVars[k]; v != "" {
				fmt.Printf("      %s=%s\n", k, v)
			}
		}
	}
	if s.Installed {
		fmt.Println("\noverall: installed")
		fmt.Println("newly launched agents should be captured at the verified tier")
		return 0
	}
	if s.ProfileBlock || s.PlistExists || s.CAExists {
		fmt.Println("\noverall: partially installed")
		switch {
		case s.ProfileBlock && s.PlistExists && !s.CAExists:
			fmt.Println("the install is present; the daemon has not created the local CA yet")
			fmt.Println("open Agent Observatory or wait a moment, then run `agents status` again")
		default:
			fmt.Println("run `agents install` to repair, or `agents uninstall` to remove the partial setup")
		}
		return 1
	}
	fmt.Println("\noverall: not fully installed")
	fmt.Println("run `agents install` to capture newly launched agents automatically")
	return 1
}
