// Package wire implements the VERIFIED evidence tier: a TLS-terminating MITM
// proxy that observes the assembled system prompt + tool schema in an agent's
// outbound LLM request, then forwards it upstream so the agent keeps working.
//
// Design:
//   - Routing is delivered by the NetworkExtension transparent proxy, which
//     sends only allowlisted provider :443 flows to this proxy; there is no
//     global HTTPS_PROXY hijack.
//   - Trust: leaf certs are signed by a local CA trusted in the user's LOGIN
//     keychain (never the System keychain) — honored by rustls (Codex) and the
//     AWS Go SDK (Bedrock) — plus the additive NODE_EXTRA_CA_CERTS for Node,
//     which does not consult the keychain by default.
//   - SigV4: for Bedrock we forward the body byte-identical, preserving every
//     signed canonical element, so the original signature still validates (no
//     re-signing, no AWS creds in the proxy). See proxy.go.
//
// This file owns the CA + per-host leaf cert minting needed to terminate TLS
// via HTTP CONNECT. Ephemeral run modes keep the CA in memory; the ambient
// install persists a stable CA so trust survives daemon restarts.
package wire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CA is a certificate authority used to mint per-host leaf certs for TLS
// interception. Ephemeral run modes keep it in memory; ambient install persists
// it under Observatory's state dir. Its cert is trusted only through injected
// process env, never system-wide.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	pemPath string

	mu    sync.Mutex
	cache map[string]*tls.Certificate // host -> minted leaf
}

// caCommonName is the subject CN of Observatory's local CA. The trust CLI looks
// it up by this name (`security find-certificate -c`), so keep them in sync.
const caCommonName = "Agent Observatory Local CA"

// caTemplate builds the x509 template for a self-signed CA valid from notBefore
// for the given lifetime, with a random serial.
func caTemplate(notBefore time.Time, lifetime time.Duration) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName, Organization: []string{"agent-observatory"}},
		NotBefore:             notBefore.Add(-24 * time.Hour),
		NotAfter:              notBefore.Add(lifetime),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
	}, nil
}

// NewCA generates a throwaway CA and writes only its certificate PEM to dir (so
// a child process can trust it via NODE_EXTRA_CA_CERTS / SSL_CERT_FILE). In this
// ephemeral mode, the private key never leaves memory.
func NewCA(dir string, notBefore time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl, err := caTemplate(notBefore, 365*24*time.Hour)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	pemPath := filepath.Join(dir, "observatory-ca.pem")
	if err := os.WriteFile(pemPath, certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA pem: %w", err)
	}

	return &CA{
		cert: cert, key: key, certPEM: certPEM, pemPath: pemPath,
		cache: map[string]*tls.Certificate{},
	}, nil
}

// LoadOrCreateCA returns a STABLE CA persisted under dir (cert PEM + private key
// PEM). If both files exist and parse, they're reused — so a long-lived daemon
// keeps the same CA across restarts and the env-injected trust stays valid. The
// key is stored 0600. Used by the ambient install; per-launch flows keep NewCA.
func LoadOrCreateCA(dir string, notBefore time.Time) (*CA, error) {
	pemPath := filepath.Join(dir, "observatory-ca.pem")
	keyPath := filepath.Join(dir, "observatory-ca.key")

	if certPEM, err := os.ReadFile(pemPath); err == nil {
		if keyPEM, err := os.ReadFile(keyPath); err == nil {
			// Reuse only a CA that is still a valid signing root for `notBefore`.
			// An expired or clock-skewed CA would silently mint leaves no client
			// accepts, so fall through and regenerate instead.
			if ca, ok := parseCA(certPEM, keyPEM, pemPath); ok && ca.usableAt(notBefore) {
				return ca, nil
			}
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl, err := caTemplate(notBefore, 5*365*24*time.Hour)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(pemPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("write CA pem: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{cert: cert, key: key, certPEM: certPEM, pemPath: pemPath, cache: map[string]*tls.Certificate{}}, nil
}

// usableAt reports whether the CA is a valid signing root at t — it must be a
// CA cert and t must fall well inside its validity window. We require a 30-day
// margin before expiry so a long session can't outlive the leaves it mints.
func (c *CA) usableAt(t time.Time) bool {
	return c.cert != nil && c.cert.IsCA &&
		!t.Before(c.cert.NotBefore) &&
		t.Add(30*24*time.Hour).Before(c.cert.NotAfter)
}

func parseCA(certPEM, keyPEM []byte, pemPath string) (*CA, bool) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, false
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, false
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, false
	}
	return &CA{cert: cert, key: key, certPEM: certPEM, pemPath: pemPath, cache: map[string]*tls.Certificate{}}, true
}

// PEMPath is the on-disk path of the CA cert, for injecting into the child's
// trust env (NODE_EXTRA_CA_CERTS / SSL_CERT_FILE / AWS_CA_BUNDLE).
func (c *CA) PEMPath() string { return c.pemPath }

// LeafFor mints (and caches) a leaf certificate for the given host, signed by the
// CA, so the proxy can present it when terminating TLS for that host.
func (c *CA) LeafFor(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cert, ok := c.cache[host]; ok {
		return cert, nil
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    c.cert.NotBefore,
		NotAfter:     c.cert.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	// Host may be an IP (loopback in tests) or a DNS name (real upstreams).
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &leafKey.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	tlsCert := &tls.Certificate{
		Certificate: [][]byte{der, c.cert.Raw},
		PrivateKey:  leafKey,
		Leaf:        mustParse(der),
	}
	c.cache[host] = tlsCert
	return tlsCert, nil
}

func mustParse(der []byte) *x509.Certificate {
	c, _ := x509.ParseCertificate(der)
	return c
}
