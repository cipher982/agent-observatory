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
	fmt.Println("  shell environment:", t.ProfilePath, "+ launchctl setenv")
	fmt.Println()
	fmt.Println("New terminals and newly launched agents will route through the local proxy.")
	fmt.Println("Already-running shells won't be captured until restarted.")
	fmt.Println("Remove everything with:  agents uninstall")
	return 0
}

func runUninstall(args []string) int {
	t, err := realTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		return 1
	}
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
	fmt.Printf("  %s shell env block   %s\n", mark(s.ProfileBlock), t.ProfilePath)
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
		fmt.Println("new agents should be captured at the verified tier")
		return 0
	}
	fmt.Println("\noverall: not fully installed")
	fmt.Println("run `agents install` for ambient capture, or use `agents monitor` for a one-off proxy")
	return 1
}
