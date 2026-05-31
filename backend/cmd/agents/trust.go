package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// runTrust manages the local CA's trust in the macOS login keychain. This is the
// NetworkExtension capture path's trust delivery: the NE system extension routes
// provider flows to the local Go MITM proxy, which presents leaf certs minted by
// our CA. For agents to accept those certs WITHOUT a global env-var hijack, the
// CA must be trusted in the user's login keychain.
//
// This trust is installed only behind the user-approved system extension, and
// `agents trust remove` (also run by uninstall) reverses it.
//
//	agents trust install   add the CA to the login keychain as SSL-trusted
//	agents trust remove    delete the CA from the login keychain
//	agents trust status    report whether the CA is currently trusted
//
// We operate on the LOGIN keychain (per-user), never the System keychain, so no
// admin escalation is required and the blast radius is the user's own trust only.
func runTrust(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agents trust install|remove|status")
		return 2
	}
	action := args[0]
	rest := args[1:]

	var caPath string
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.StringVar(&caPath, "ca", "", "path to the CA PEM (defaults to the installed daemon CA)")
	_ = fs.Parse(rest)

	t, err := realTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "trust: %v\n", err)
		return 1
	}
	if caPath == "" {
		caPath = t.CAPEMPublic()
	}

	switch action {
	case "install":
		return trustInstall(caPath)
	case "remove":
		return trustRemove(caPath)
	case "status":
		return trustStatus(caPath)
	default:
		fmt.Fprintf(os.Stderr, "trust: unknown action %q (want install|remove|status)\n", action)
		return 2
	}
}

func loginKeychain() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/Library/Keychains/login.keychain-db", nil
}

func trustInstall(caPath string) int {
	if _, err := os.Stat(caPath); err != nil {
		fmt.Fprintf(os.Stderr, "trust install: CA not found at %s (is the daemon installed?): %v\n", caPath, err)
		return 1
	}
	kc, err := loginKeychain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "trust install: %v\n", err)
		return 1
	}
	// add-trusted-cert with -r trustAsRoot on the LOGIN keychain (no sudo). The
	// user is prompted once by the Security framework to authorize the change.
	cmd := exec.Command("security", "add-trusted-cert",
		"-r", "trustAsRoot",
		"-p", "ssl",
		"-k", kc,
		caPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "trust install: %v\n", err)
		return 1
	}
	fmt.Printf("trust: CA installed in login keychain (%s)\n", caPath)
	return 0
}

func trustRemove(caPath string) int {
	if _, err := os.Stat(caPath); err != nil {
		// CA file already gone (e.g. uninstall ran first); nothing to remove by file.
		fmt.Printf("trust: CA file absent, nothing to remove\n")
		return 0
	}
	cmd := exec.Command("security", "remove-trusted-cert", caPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Non-fatal: the cert may not have been trusted.
		fmt.Fprintf(os.Stderr, "trust remove (non-fatal): %v\n", err)
		return 0
	}
	fmt.Println("trust: CA trust removed from login keychain")
	return 0
}

func trustStatus(caPath string) int {
	if _, err := os.Stat(caPath); err != nil {
		fmt.Printf("trust: no CA file at %s\n", caPath)
		return 0
	}
	// verify-cert against the CA itself is noisy; just report presence + a hint.
	fmt.Printf("trust: CA present at %s; run `agents trust install` to trust it\n", caPath)
	return 0
}
