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

// caCommonName must match the CA subject CN minted in wire.NewCA /
// wire.LoadOrCreateCA, so `security find-certificate -c` can locate it.
const caCommonName = "Agent Observatory Local CA"

// caAlreadyTrusted reports whether our CA is present in the login keychain. The
// only way add-trusted-cert errors with "parameters not valid" is when the cert
// already exists with trust settings, so presence here means trust is in place.
func caAlreadyTrusted(keychain string) bool {
	return exec.Command("security", "find-certificate", "-c", caCommonName, keychain).Run() == nil
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
	// Idempotent: re-running add-trusted-cert on an ALREADY-trusted cert fails with
	// "SecTrustSettingsSetTrustSettings: One or more parameters … not valid". Since
	// re-enabling capture re-runs this, treat an already-trusted CA as success.
	if caAlreadyTrusted(kc) {
		fmt.Printf("trust: CA already trusted in login keychain (%s)\n", caPath)
		return 0
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
		// A second-chance check: the cert may already be trusted (the error above
		// is exactly what an already-trusted cert returns), in which case we're done.
		if caAlreadyTrusted(kc) {
			fmt.Printf("trust: CA already trusted in login keychain (%s)\n", caPath)
			return 0
		}
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
	kc, err := loginKeychain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "trust status: %v\n", err)
		return 1
	}
	// Is OUR CA actually present in the login keychain? Match by the CA's common
	// name; `security find-certificate -c` exits non-zero when absent.
	found := exec.Command("security", "find-certificate", "-c", caCommonName, kc).Run() == nil
	if found {
		fmt.Printf("trust: CA is trusted in the login keychain (%s)\n", caPath)
	} else {
		fmt.Printf("trust: CA present on disk but not in the login keychain; run `agents trust install`\n")
	}
	return 0
}
