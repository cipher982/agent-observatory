// Command devharness is a DEV-ONLY test client for safe, scoped iteration on the
// NetworkExtension transparent proxy. It makes one HTTPS request to a provider
// host, trusting the local Observatory CA — i.e. it behaves like a correctly
// configured agent. The NE extension's dev-scope allowlist
// (/tmp/agent-observatory-dev-scope) is set to this binary's signing identifier
// so that ONLY this process is ever intercepted; the developer's real agents are
// untouched.
//
//	devharness [url]   default url: https://api.openai.com/v1/models
//
// A real provider response (e.g. HTTP 401) proves the proxy forwarded to the
// real upstream — i.e. the routing loop is fixed. HTTP 502 / a TLS error means
// the proxy failed (e.g. the upstream forward looped back).
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	url := "https://api.openai.com/v1/models"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}

	home, _ := os.UserHomeDir()
	caPath := filepath.Join(home, ".local", "state", "agent-observatory", "ca", "observatory-ca.pem")
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if pem, err := os.ReadFile(caPath); err == nil {
		if roots.AppendCertsFromPEM(pem) {
			fmt.Printf("devharness: trusting local CA %s\n", caPath)
		}
	} else {
		fmt.Printf("devharness: WARNING no local CA at %s (%v)\n", caPath, err)
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		Timeout:   15 * time.Second,
	}
	fmt.Printf("devharness: GET %s\n", url)
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("devharness: REQUEST FAILED: %v\n", err)
		fmt.Println("  (a TLS error here = the proxy presented an untrusted/looping cert)")
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	fmt.Printf("devharness: HTTP %d (a real provider status like 401 = forward works; 502 = proxy loop/fail)\n", resp.StatusCode)
	fmt.Printf("devharness: body[:256]=%q\n", body)
}
