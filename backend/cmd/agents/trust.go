package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// caAlreadyTrusted reports whether the EXACT current CA (matched by SHA-256
// fingerprint, not just common name) is present in the login keychain. Matching
// by fingerprint avoids a stale same-CN cert masquerading as trusted while the
// daemon actually signs with a different key.
func caAlreadyTrusted(keychain, caPath string) bool {
	want, err := certSHA256(caPath)
	if err != nil {
		return false
	}
	// -Z prints the SHA-256 of each matching cert; -a returns all (handles dupes).
	out, err := exec.Command("security", "find-certificate", "-a", "-c", caCommonName, "-Z", keychain).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), want)
}

// certSHA256 returns the uppercase hex SHA-256 of the DER cert at path.
func certSHA256(path string) (string, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", path)
	}
	sum := sha256.Sum256(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
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
	if caAlreadyTrusted(kc, caPath) {
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
		if caAlreadyTrusted(kc, caPath) {
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
	// Remove trust settings for the current CA file (if it still exists)...
	if _, err := os.Stat(caPath); err == nil {
		cmd := exec.Command("security", "remove-trusted-cert", caPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "trust remove (non-fatal): %v\n", err)
		}
	}
	// ...then sweep EVERY Observatory CA out of the login keychain by SHA-256 hash
	// (and its trust settings via -t). Re-installs over time can leave multiple
	// same-CN roots trusted; delete-certificate -c refuses an ambiguous name, so
	// we enumerate hashes and delete each. Uninstall must clear ALL of them.
	if kc, err := loginKeychain(); err == nil {
		for _, hash := range observatoryCAHashes(kc) {
			if err := exec.Command("security", "delete-certificate", "-Z", hash, "-t", kc).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "trust remove (non-fatal): could not delete %s: %v\n", hash, err)
			}
		}
	}
	fmt.Println("trust: CA trust removed from login keychain")
	return 0
}

// observatoryCAHashes returns the SHA-256 hashes of every cert named caCommonName
// in the given keychain.
func observatoryCAHashes(keychain string) []string {
	out, err := exec.Command("security", "find-certificate", "-a", "-c", caCommonName, "-Z", keychain).Output()
	if err != nil {
		return nil
	}
	var hashes []string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA-256 hash: "); ok {
			hashes = append(hashes, strings.TrimSpace(rest))
		}
	}
	return hashes
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
	// Is the EXACT current CA (by fingerprint) trusted in the login keychain?
	if caAlreadyTrusted(kc, caPath) {
		fmt.Printf("trust: CA is trusted in the login keychain (%s)\n", caPath)
	} else {
		fmt.Printf("trust: CA present on disk but not in the login keychain; run `agents trust install`\n")
	}
	return 0
}
