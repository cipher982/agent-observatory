package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	headerTransparentFlow = "X-Agent-Observatory-Transparent-Flow"
	headerSourceSigningID = "X-Agent-Observatory-Source-Signing-ID"
	headerSourcePID       = "X-Agent-Observatory-Source-PID"
)

type coverage struct {
	Captures       int `json:"captures"`
	Bypasses       int `json:"bypasses"`
	RecentBypasses []struct {
		Host    string `json:"host"`
		Runtime string `json:"runtime"`
		Reason  string `json:"reason"`
		Source  string `json:"source"`
		At      string `json:"at"`
	} `json:"recentBypasses"`
}

func main() {
	var (
		apiURL   string
		proxyURL string
		caPath   string
	)
	flag.StringVar(&apiURL, "api", "http://127.0.0.1:7878", "monitor API base URL")
	flag.StringVar(&proxyURL, "proxy", "http://127.0.0.1:7879", "wire proxy URL")
	flag.StringVar(&caPath, "ca", "", "Observatory CA PEM path")
	flag.Parse()

	if caPath == "" {
		fmt.Fprintln(os.Stderr, "wirepolicyqa: --ca is required")
		os.Exit(2)
	}

	before, _ := readCoverage(apiURL)

	if err := providerRequest(proxyURL, caPath, "codex", true); err != nil {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: supported-source request failed: %v\n", err)
		os.Exit(1)
	}
	if err := providerRequest(proxyURL, caPath, "com.example.observatory-qa-custom-tool", false); err != nil {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: unknown-source request failed: %v\n", err)
		os.Exit(1)
	}

	var after coverage
	var err error
	for i := 0; i < 20; i++ {
		after, err = readCoverage(apiURL)
		if err == nil && after.Captures > before.Captures && after.Bypasses > before.Bypasses {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: coverage read failed: %v\n", err)
		os.Exit(1)
	}
	if after.Captures != before.Captures+1 {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: captures = %d, want exactly %d (one supported-source capture and no unknown-source body capture)\n", after.Captures, before.Captures+1)
		os.Exit(1)
	}
	if after.Bypasses != before.Bypasses+1 {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: bypasses = %d, want exactly %d (one unknown-source pass-through)\n", after.Bypasses, before.Bypasses+1)
		os.Exit(1)
	}

	var sawUnknownBypass bool
	for _, b := range after.RecentBypasses {
		if b.Source == "com.example.observatory-qa-custom-tool" && strings.Contains(b.Reason, "unsupported") {
			sawUnknownBypass = true
			break
		}
	}
	if !sawUnknownBypass {
		fmt.Fprintf(os.Stderr, "wirepolicyqa: coverage did not include expected unknown-source bypass: %+v\n", after.RecentBypasses)
		os.Exit(1)
	}

	fmt.Printf("wirepolicyqa: OK (captures %d→%d, bypasses %d→%d)\n", before.Captures, after.Captures, before.Bypasses, after.Bypasses)
}

func providerRequest(proxyRaw, caPath, source string, trustObservatory bool) error {
	proxy, err := url.Parse(proxyRaw)
	if err != nil {
		return err
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if trustObservatory {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return fmt.Errorf("read CA: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("could not append Observatory CA")
		}
	}
	tr := &http.Transport{
		Proxy: http.ProxyURL(proxy),
		ProxyConnectHeader: http.Header{
			headerTransparentFlow: {"1"},
			headerSourceSigningID: {source},
			headerSourcePID:       {fmt.Sprint(os.Getpid())},
		},
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	body := []byte(`{"instructions":"observatory qa system","input":[{"role":"user","content":[{"type":"input_text","text":"observatory qa user"}]}],"tools":[{"type":"function","name":"observatory_qa_tool"}],"model":"gpt-5.5"}`)
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer observatory-qa-no-key")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 400 || resp.StatusCode > 499 {
		return fmt.Errorf("provider status = %d, want 4xx auth/client response", resp.StatusCode)
	}
	return nil
}

func readCoverage(apiURL string) (coverage, error) {
	resp, err := http.Get(strings.TrimRight(apiURL, "/") + "/api/coverage")
	if err != nil {
		return coverage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return coverage{}, fmt.Errorf("coverage status %d", resp.StatusCode)
	}
	var c coverage
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return coverage{}, err
	}
	return c, nil
}
