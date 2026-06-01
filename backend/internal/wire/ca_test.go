package wire

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStableCAReusedWhenValid: a second load with the same dir reuses the exact
// same certificate (so env/keychain trust stays valid across daemon restarts).
func TestStableCAReusedWhenValid(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)

	first, err := LoadOrCreateCA(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateCA(dir, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.cert.Raw, second.cert.Raw) {
		t.Fatal("stable CA was not reused across loads")
	}
}

// TestLeafCertValidityWithinAppleLimit: minted leaves must be valid for 825 days
// or fewer, or the macOS system verifier rejects them (Apple's post-2019 rule).
// Regression guard for the bug where leaves inherited the CA's 5-year lifetime.
func TestLeafCertValidityWithinAppleLimit(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.LeafFor("api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	validity := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore)
	if validity > 825*24*time.Hour {
		t.Errorf("leaf validity %v exceeds Apple's 825-day limit", validity)
	}
	if len(leaf.Leaf.DNSNames) == 0 {
		t.Error("leaf must carry a SAN dNSName (Apple requires SAN, not just CN)")
	}
}

// TestExpiredStableCAIsRegenerated: an on-disk CA that's no longer usable at the
// current time must be replaced, not reused — otherwise it silently mints leaves
// no client will accept.
func TestExpiredStableCAIsRegenerated(t *testing.T) {
	dir := t.TempDir()

	// Create a CA anchored far in the past so it's expired relative to "now".
	old := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	stale, err := LoadOrCreateCA(dir, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "observatory-ca.key")); err != nil {
		t.Fatalf("expected key on disk: %v", err)
	}

	now := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	if stale.usableAt(now) {
		t.Fatal("test precondition: stale CA should not be usable now")
	}

	fresh, err := LoadOrCreateCA(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(fresh.cert.Raw, stale.cert.Raw) {
		t.Fatal("expired CA was reused instead of regenerated")
	}
	if !fresh.usableAt(now) {
		t.Fatal("regenerated CA should be usable now")
	}
}
