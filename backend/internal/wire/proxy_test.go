package wire

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
)

// TestParseBodyVariants checks every endpoint shape parses, including Bedrock.
func TestParseBodyVariants(t *testing.T) {
	cases := []struct {
		name, host, path, body string
		wantTool               bool
		wantSystem             string
	}{
		{
			name: "anthropic-native", host: "api.anthropic.com", path: "/v1/messages",
			body:       `{"system":"you are...project instructions...","tools":[{"name":"x"}],"messages":[]}`,
			wantTool:   true,
			wantSystem: "you are...project instructions...",
		},
		{
			name: "bedrock-invoke", host: "bedrock-runtime.us-east-1.amazonaws.com",
			path:     "/model/global.anthropic.claude-opus-4-8/invoke-with-response-stream",
			body:     `{"anthropic_version":"bedrock-2023-05-31","system":[{"type":"text","text":"project instructions here"}],"tools":[{"name":"mcp__search__query"}],"messages":[]}`,
			wantTool: true, wantSystem: "project instructions here",
		},
		{
			name: "aws-external-anthropic", host: "aws-external-anthropic.us-east-1.api.aws",
			path:     "/v1/messages",
			body:     `{"system":"x","tools":[{"name":"t"}],"messages":[{"role":"user","content":"...project instructions..."}]}`,
			wantTool: true, wantSystem: "x",
		},
		{
			name: "openai-chat", host: "api.openai.com", path: "/v1/chat/completions",
			body:     `{"messages":[{"role":"system","content":"project instructions"}],"tools":[{"type":"function","function":{"name":"f"}}]}`,
			wantTool: true, wantSystem: "project instructions",
		},
		{
			name: "codex-responses", host: "api.openai.com", path: "/v1/responses",
			body:     `{"instructions":"sys","input":[{"role":"user","content":[{"type":"input_text","text":"...project instructions..."}]}],"tools":[{"type":"function","name":"exec"}]}`,
			wantTool: true, wantSystem: "sys",
		},
		{
			name: "gemini-generate-content", host: "generativelanguage.googleapis.com", path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			body:     `{"systemInstruction":{"parts":[{"text":"gemini system"}]},"contents":[{"role":"user","parts":[{"text":"project instructions"}]}],"tools":[{"functionDeclarations":[{"name":"read_file"}]}]}`,
			wantTool: true, wantSystem: "gemini system",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cap, ok := ParseBody(c.host, c.path, []byte(c.body))
			if !ok {
				t.Fatalf("ParseBody failed to recognize %s", c.name)
			}
			if (len(cap.ToolNames) > 0) != c.wantTool {
				t.Errorf("tools = %v, wantTool=%v", cap.ToolNames, c.wantTool)
			}
			if cap.SystemPrompt != c.wantSystem {
				t.Errorf("system = %q, want %q", cap.SystemPrompt, c.wantSystem)
			}
			if cap.AllText == "" {
				t.Errorf("all text should be populated")
			}
		})
	}
}

// TestEndToEndInterception is the real proof: a TLS client configured to trust
// the proxy's CA sends an HTTPS request THROUGH the proxy (via CONNECT) to a
// local upstream TLS server; we assert the proxy captured the body AND the
// upstream received it byte-identical (forward fidelity → SigV4 would survive).
func TestEndToEndInterception(t *testing.T) {
	tmp := t.TempDir()

	// 1) Upstream: a real TLS server that echoes whether it got the exact body.
	const instructionText = "Run release smoke tests before launch."
	const reqBody = `{"system":"sys with Run release smoke tests before launch.","tools":[{"name":"mcp__reviewer__ask"}],"messages":[]}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)
	upHost, upPort, _ := net.SplitHostPort(upURL.Host)

	// 2) Proxy server with ephemeral CA.
	// notBefore anchored near "now"; the CA is valid ±a year so tests are date-robust.
	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	// Override leaf minting to use the upstream's hostname (127.0.0.1) so the
	// client's SNI/cert check passes when we dial the real upstream below.
	// The proxy must trust the (self-signed) test upstream when it forwards.
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})

	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// 3) Build a client whose proxy is our server and whose trusted roots include
	//    BOTH our CA (to accept the intercepted leaf) and the upstream's cert
	//    (so the proxy->upstream dial in forward() succeeds).
	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	roots.AddCert(upstream.Certificate())

	// The proxy's forward() dials host:443, but our upstream is on a random port.
	// Redirect by sending the request to host:port and having the proxy honor it:
	// we make the client CONNECT to the upstream's real host:port.
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}

	// Use the upstream's actual host:port as the request target so CONNECT carries
	// it; the proxy mints a leaf for that host and forwards to the same host:port.
	target := fmt.Sprintf("https://%s:%s/v1/messages", upHost, upPort)
	req, _ := http.NewRequest("POST", target, bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client through proxy failed: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	// 4a) Upstream must have received the body BYTE-IDENTICAL (forward fidelity).
	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}

	// 4b) The proxy must have CAPTURED + parsed the request.
	caps := srv.Captures()
	if len(caps) == 0 {
		t.Fatalf("proxy captured no requests")
	}
	c := caps[0]
	if c.AllText == "" {
		t.Errorf("capture missed assembled request text")
	}
	if len(c.ToolNames) != 1 || c.ToolNames[0] != "mcp__reviewer__ask" {
		t.Errorf("capture tools = %v, want [mcp__reviewer__ask]", c.ToolNames)
	}

	// 4c) The capture must convert into a VERIFIED observation.
	res := testResolution(t, tmp, instructionText)
	obs := srv.ObservationsForResolution("claude", "sess-1", res)
	var verifiedDoctrine bool
	for _, o := range obs {
		if o.Source == "wire" && o.Level == fact.Verified && o.Polarity == fact.Present && o.Key.Kind == fact.InstructionText {
			verifiedDoctrine = true
		}
	}
	if !verifiedDoctrine {
		t.Errorf("expected a VERIFIED present observation from wire capture, got %+v", obs)
	}

	// 4d) The forwarded request must have produced a valid upstream response.
	if resp.StatusCode != 200 {
		t.Errorf("upstream status via proxy = %d, want 200", resp.StatusCode)
	}

	// Verify the CA PEM was written for child-trust injection.
	if _, err := os.Stat(srv.CAPath()); err != nil {
		t.Errorf("CA pem not written: %v", err)
	}
}

func TestNonProviderHostTunnelsWithoutCapture(t *testing.T) {
	tmp := t.TempDir()
	const reqBody = `{"system":"should stay opaque to proxy"}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post(upURL.String()+"/v1/messages", "application/json", bytes.NewReader([]byte(reqBody)))
	if err != nil {
		t.Fatalf("client through tunnel failed: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	if caps := srv.Captures(); len(caps) != 0 {
		t.Fatalf("non-provider host should be tunneled without capture, got %+v", caps)
	}
}

type fakeProcessLookup map[int]processInfo

func (f fakeProcessLookup) LookupProcess(pid int) (processInfo, error) {
	if p, ok := f[pid]; ok {
		return p, nil
	}
	return processInfo{Env: map[string]string{}}, nil
}

func TestTransparentSupportedSourceIsCaptured(t *testing.T) {
	tmp := t.TempDir()
	const reqBody = `{"system":"captured safe source","tools":[{"name":"safe_tool"}],"messages":[]}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)
	upHost, upPort, _ := net.SplitHostPort(upURL.Host)

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	srv.SetCapturePolicy(&CapturePolicy{
		caPath: srv.CAPath(),
		lookup: fakeProcessLookup{
			1234: {Command: "claude NODE_EXTRA_CA_CERTS=" + srv.CAPath(), Env: map[string]string{"NODE_EXTRA_CA_CERTS": srv.CAPath()}},
		},
	})
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	roots.AddCert(upstream.Certificate())
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			ProxyConnectHeader: http.Header{
				headerTransparentFlow: {"1"},
				headerSourceSigningID: {"com.anthropic.claude-code"},
				headerSourcePID:       {"1234"},
			},
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}

	target := fmt.Sprintf("https://%s:%s/v1/messages", upHost, upPort)
	resp, err := client.Post(target, "application/json", bytes.NewReader([]byte(reqBody)))
	if err != nil {
		t.Fatalf("trusted transparent source failed: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	caps := srv.Captures()
	if len(caps) != 1 {
		t.Fatalf("captures = %d, want 1", len(caps))
	}
	if caps[0].SystemPrompt != "captured safe source" {
		t.Fatalf("system prompt = %q", caps[0].SystemPrompt)
	}
}

func TestTransparentUnknownProviderSourceTunnelsWithoutCapture(t *testing.T) {
	tmp := t.TempDir()
	const reqBody = `{"system":"must stay opaque"}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	srv.SetCapturePolicy(&CapturePolicy{caPath: srv.CAPath(), lookup: fakeProcessLookup{}})
	bypassCh, unsubBypass := srv.SubscribeBypasses()
	defer unsubBypass()
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			ProxyConnectHeader: http.Header{
				headerTransparentFlow: {"1"},
				headerSourceSigningID: {"com.example.custom-tool"},
				headerSourcePID:       {"9999"},
			},
			// Deliberately do NOT trust the Observatory CA. If the proxy tries to
			// MITM this unknown source, the request fails. The safe behavior is an
			// opaque tunnel to the upstream's real cert.
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post(upURL.String()+"/v1/messages", "application/json", bytes.NewReader([]byte(reqBody)))
	if err != nil {
		t.Fatalf("unknown transparent source should tunnel, got error: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	if caps := srv.Captures(); len(caps) != 0 {
		t.Fatalf("unknown transparent source should not be captured, got %+v", caps)
	}
	bypasses := srv.Bypasses()
	if len(bypasses) != 1 {
		t.Fatalf("bypasses = %d, want 1", len(bypasses))
	}
	if !strings.Contains(bypasses[0].Reason, "unsupported") {
		t.Fatalf("bypass reason = %q, want unsupported source", bypasses[0].Reason)
	}
	if bypasses[0].SourceSigningID != "com.example.custom-tool" {
		t.Fatalf("bypass source = %q", bypasses[0].SourceSigningID)
	}
	select {
	case ev := <-bypassCh:
		if ev.SourceSigningID != "com.example.custom-tool" {
			t.Fatalf("subscribed bypass source = %q", ev.SourceSigningID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscribed bypass event")
	}
}

func TestStableServerMissingTransparentMetadataTunnelsWithoutCapture(t *testing.T) {
	tmp := t.TempDir()
	const reqBody = `{"system":"old extension or malformed relay must stay opaque"}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)

	srv, err := NewServerStableCA(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			// No transparent metadata headers and no Observatory CA trust. This
			// models a stale pre-0.3 extension talking to the 0.3 installed daemon.
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
		Timeout: 10 * time.Second,
	}

	resp, err := client.Post(upURL.String()+"/v1/messages", "application/json", bytes.NewReader([]byte(reqBody)))
	if err != nil {
		t.Fatalf("stable daemon without source metadata should tunnel, got error: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	if caps := srv.Captures(); len(caps) != 0 {
		t.Fatalf("missing metadata should not be captured in stable mode, got %+v", caps)
	}
	bypasses := srv.Bypasses()
	if len(bypasses) != 1 {
		t.Fatalf("bypasses = %d, want 1", len(bypasses))
	}
	if !strings.Contains(bypasses[0].Reason, "missing transparent source metadata") {
		t.Fatalf("bypass reason = %q", bypasses[0].Reason)
	}
}

func TestTransparentSupportedSourceWithStaleTrustTunnelsWithoutCapture(t *testing.T) {
	cases := []struct {
		name, signingID, command, envKey, envValue, runtime string
	}{
		{
			name:      "claude",
			signingID: "com.anthropic.claude-code",
			command:   "claude NODE_EXTRA_CA_CERTS=/tmp/old-ca.pem",
			envKey:    "NODE_EXTRA_CA_CERTS",
			envValue:  "/tmp/old-ca.pem",
			runtime:   "claude",
		},
		{
			name:      "codex",
			signingID: "codex",
			command:   "codex CODEX_CA_CERTIFICATE=/tmp/old-ca.pem",
			envKey:    "CODEX_CA_CERTIFICATE",
			envValue:  "/tmp/old-ca.pem",
			runtime:   "codex",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			upURL, _ := url.Parse(upstream.URL)

			srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			srv.SetInspectHost(func(string) bool { return true })
			srv.SetCapturePolicy(&CapturePolicy{
				caPath: srv.CAPath(),
				lookup: fakeProcessLookup{
					1234: {Command: tc.command, Env: map[string]string{tc.envKey: tc.envValue}},
				},
			})
			proxyAddr, err := srv.Listen(0)
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()

			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			proxyURL, _ := url.Parse("http://" + proxyAddr)
			client := &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
					ProxyConnectHeader: http.Header{
						headerTransparentFlow: {"1"},
						headerSourceSigningID: {tc.signingID},
						headerSourcePID:       {"1234"},
					},
					// Deliberately do NOT trust the Observatory CA. If a stale
					// runtime is inspected instead of tunneled, this request fails.
					TLSClientConfig: &tls.Config{RootCAs: roots},
				},
				Timeout: 10 * time.Second,
			}

			resp, err := client.Post(upURL.String()+"/v1/messages", "application/json", bytes.NewReader([]byte(`{"system":"stale"}`)))
			if err != nil {
				t.Fatalf("stale %s runtime should tunnel, got error: %v", tc.name, err)
			}
			defer resp.Body.Close()
			_, _ = io.ReadAll(resp.Body)
			if caps := srv.Captures(); len(caps) != 0 {
				t.Fatalf("stale %s runtime should not be captured, got %+v", tc.name, caps)
			}
			bypasses := srv.Bypasses()
			if len(bypasses) != 1 {
				t.Fatalf("bypasses = %d, want 1", len(bypasses))
			}
			if !strings.Contains(bypasses[0].Reason, tc.envKey+" missing or stale") {
				t.Fatalf("bypass reason = %q, want stale %s", bypasses[0].Reason, tc.envKey)
			}
			if bypasses[0].Runtime != tc.runtime {
				t.Fatalf("bypass runtime = %q, want %s", bypasses[0].Runtime, tc.runtime)
			}
			if bypasses[0].SourceSigningID != tc.signingID {
				t.Fatalf("bypass source = %q, want %s", bypasses[0].SourceSigningID, tc.signingID)
			}
		})
	}
}

func TestTransparentPolicyRecognizesCodexAndGemini(t *testing.T) {
	const ca = "/tmp/observatory-ca.pem"
	policy := &CapturePolicy{caPath: ca, lookup: fakeProcessLookup{
		10: {Command: "codex CODEX_CA_CERTIFICATE=" + ca, Env: map[string]string{"CODEX_CA_CERTIFICATE": ca}},
		11: {Command: "/opt/homebrew/bin/node /opt/homebrew/bin/gemini NODE_EXTRA_CA_CERTS=" + ca, Env: map[string]string{"NODE_EXTRA_CA_CERTS": ca}},
		12: {Command: "/opt/homebrew/bin/node /tmp/custom.js NODE_EXTRA_CA_CERTS=" + ca, Env: map[string]string{"NODE_EXTRA_CA_CERTS": ca}},
	}}
	if got := policy.Decide(FlowMetadata{Transparent: true, SourceSigningID: "codex", SourcePID: 10}); !got.inspect || got.runtime != "codex" {
		t.Fatalf("codex decision = %+v, want inspect", got)
	}
	if got := policy.Decide(FlowMetadata{Transparent: true, SourceSigningID: "node-abcdef", SourcePID: 11}); !got.inspect || got.runtime != "gemini" {
		t.Fatalf("gemini decision = %+v, want inspect", got)
	}
	if got := policy.Decide(FlowMetadata{Transparent: true, SourceSigningID: "node-abcdef", SourcePID: 12}); got.inspect {
		t.Fatalf("custom node decision = %+v, want tunnel", got)
	}
}

func TestTransparentPolicyBypassesMissingOrStaleRuntimeTrust(t *testing.T) {
	const ca = "/tmp/observatory-ca.pem"
	cases := []struct {
		name, signingID, command, wantRuntime, wantReason string
		env                                               map[string]string
	}{
		{
			name:        "claude missing node ca",
			signingID:   "com.anthropic.claude-code",
			command:     "claude",
			wantRuntime: "claude",
			wantReason:  "NODE_EXTRA_CA_CERTS missing or stale",
		},
		{
			name:        "claude stale node ca",
			signingID:   "com.anthropic.claude-code",
			command:     "claude NODE_EXTRA_CA_CERTS=/tmp/old.pem",
			env:         map[string]string{"NODE_EXTRA_CA_CERTS": "/tmp/old.pem"},
			wantRuntime: "claude",
			wantReason:  "NODE_EXTRA_CA_CERTS missing or stale",
		},
		{
			name:        "codex missing codex ca",
			signingID:   "codex",
			command:     "codex",
			wantRuntime: "codex",
			wantReason:  "CODEX_CA_CERTIFICATE missing or stale",
		},
		{
			name:        "codex stale codex ca",
			signingID:   "codex",
			command:     "codex CODEX_CA_CERTIFICATE=/tmp/old.pem",
			env:         map[string]string{"CODEX_CA_CERTIFICATE": "/tmp/old.pem"},
			wantRuntime: "codex",
			wantReason:  "CODEX_CA_CERTIFICATE missing or stale",
		},
		{
			name:        "gemini missing node ca",
			signingID:   "node-abcdef",
			command:     "/opt/homebrew/bin/node /opt/homebrew/bin/gemini",
			wantRuntime: "gemini",
			wantReason:  "NODE_EXTRA_CA_CERTS missing or stale",
		},
		{
			name:        "gemini stale node ca",
			signingID:   "node-abcdef",
			command:     "/opt/homebrew/bin/node /opt/homebrew/bin/gemini NODE_EXTRA_CA_CERTS=/tmp/old.pem",
			env:         map[string]string{"NODE_EXTRA_CA_CERTS": "/tmp/old.pem"},
			wantRuntime: "gemini",
			wantReason:  "NODE_EXTRA_CA_CERTS missing or stale",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := &CapturePolicy{caPath: ca, lookup: fakeProcessLookup{
				i + 1: {Command: tc.command, Env: tc.env},
			}}
			got := policy.Decide(FlowMetadata{Transparent: true, SourceSigningID: tc.signingID, SourcePID: i + 1})
			if got.inspect {
				t.Fatalf("decision = %+v, want tunnel", got)
			}
			if got.runtime != tc.wantRuntime {
				t.Fatalf("runtime = %q, want %q", got.runtime, tc.wantRuntime)
			}
			if got.reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.reason, tc.wantReason)
			}
		})
	}
}

// TestStreamingResponseIsNotBuffered proves the regression fix for the buffered
// forward: an inspected upstream that streams its body in chunks (think SSE /
// stream:true) must reach the client incrementally, not be withheld until the
// whole generation completes. We have the upstream flush a first chunk, then
// block on a gate until the client confirms it already saw that chunk.
func TestStreamingResponseIsNotBuffered(t *testing.T) {
	tmp := t.TempDir()

	releaseUpstream := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		// Don't finish the response until the client proves it got chunk one.
		select {
		case <-releaseUpstream:
		case <-time.After(5 * time.Second):
			t.Error("client never confirmed the first chunk; response was buffered")
		}
		_, _ = io.WriteString(w, "data: second\n\n")
		fl.Flush()
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)
	upHost, upPort, _ := net.SplitHostPort(upURL.Host)

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	roots.AddCert(upstream.Certificate())
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}, Timeout: 10 * time.Second}

	target := fmt.Sprintf("https://%s:%s/v1/messages", upHost, upPort)
	req, _ := http.NewRequest("POST", target, bytes.NewReader([]byte(`{"system":"s","messages":[]}`)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client through proxy failed: %v", err)
	}
	defer resp.Body.Close()

	// Read just the first streamed chunk; if the proxy buffered, this blocks until
	// the upstream timeout fires and the test fails there.
	buf := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("reading first chunk: %v", err)
	}
	if string(buf) != "data: first\n\n" {
		t.Fatalf("first chunk = %q, want %q", buf, "data: first\n\n")
	}
	close(releaseUpstream)

	rest, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(rest, []byte("data: second")) {
		t.Errorf("missing second chunk after release: %q", rest)
	}
}

// TestDefaultInspectHostContract pins the provider allowlist. It MUST stay in
// sync with the Swift Allowlist (app/TransparentProxyExtension/Allowlist.swift)
// and its mirror test (app/ObservatoryTests/SNITests.swift:testAllowlistMatching),
// or the kernel would divert flows the Go side won't inspect (or vice versa).
func TestDefaultInspectHostContract(t *testing.T) {
	allow := []string{
		"api.anthropic.com",
		"api.openai.com",
		"bedrock-runtime.us-east-1.amazonaws.com",
		"aws-external-anthropic.us-east-1.api.aws",
		"API.ANTHROPIC.COM",  // case-insensitive
		"api.anthropic.com.", // trailing dot
	}
	deny := []string{
		"example.com",
		"evil-amazonaws.com",
		"amazonaws.com.attacker.com",
		"s3.amazonaws.com",                // AWS but not bedrock
		"aws-external-anthropic.evil.com", // right prefix, wrong suffix
		"notanthropic.com",
		"api.openai.com.evil.com", // not exact
	}
	for _, h := range allow {
		if !defaultInspectHost(h) {
			t.Errorf("defaultInspectHost(%q) = false, want true", h)
		}
	}
	for _, h := range deny {
		if defaultInspectHost(h) {
			t.Errorf("defaultInspectHost(%q) = true, want false", h)
		}
	}
}

func TestStableServerPausesCaptureAfterClientTLSRejectsLeaf(t *testing.T) {
	restore := SetCapturePausePathForTest(filepath.Join(t.TempDir(), "capture-paused"))
	defer restore()

	srv, err := NewServerStableCA(t.TempDir(), log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCapturePolicy(&CapturePolicy{
		caPath:                     srv.CAPath(),
		requireTransparentMetadata: true,
		lookup: fakeProcessLookup{
			1234: {Command: "codex CODEX_CA_CERTIFICATE=" + srv.CAPath(), Env: map[string]string{"CODEX_CA_CERTIFICATE": srv.CAPath()}},
		},
	})
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	const host = "api.openai.com"
	fmt.Fprintf(raw,
		"CONNECT %s:443 HTTP/1.1\r\n"+
			"Host: %s:443\r\n"+
			"%s: 1\r\n"+
			"%s: codex\r\n"+
			"%s: 1234\r\n"+
			"\r\n",
		host, host,
		headerTransparentFlow,
		headerSourceSigningID,
		headerSourcePID,
	)
	connBR := bufio.NewReader(raw)
	if r, err := http.ReadResponse(connBR, &http.Request{Method: "CONNECT"}); err != nil || r.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, r)
	}

	tlsConn := tls.Client(raw, &tls.Config{ServerName: host})
	if err := tlsConn.Handshake(); err == nil {
		t.Fatal("client TLS handshake unexpectedly trusted Observatory leaf")
	}

	deadline := time.Now().Add(time.Second)
	for {
		if paused, reason := CapturePaused(); paused {
			if !strings.Contains(reason, host) {
				t.Fatalf("pause reason = %q, want host %q", reason, host)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture pause marker was not written after client TLS failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fails, lastHost := srv.ClientTLSFailures(); fails != 1 || lastHost != host {
		t.Fatalf("ClientTLSFailures = (%d, %q), want (1, %q)", fails, lastHost, host)
	}
}

// TestWebSocketUpgradeReplies426 proves that an inspected-host WebSocket upgrade
// gets a 426 Upgrade Required, NOT a broken opaque tunnel. This is the
// source-verified mechanism that makes Codex capturable: codex maps a 426 on its
// wss://…/responses connect straight to its HTTP fallback (which we fully
// capture), with no retry delay. (A proxied 101 is rejected by strict WS clients
// as "Attack attempt detected" and yields no payload anyway.)
func TestWebSocketUpgradeReplies426(t *testing.T) {
	tmp := t.TempDir()

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// CONNECT to an arbitrary provider-ish host, TLS with our CA, send a WS upgrade.
	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	const host = "api.openai.com"
	fmt.Fprintf(raw, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
	connBR := bufio.NewReader(raw)
	connResp, err := http.ReadResponse(connBR, &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, connResp)
	}

	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	tlsConn := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: roots})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	fmt.Fprintf(tlsConn, "GET /v1/responses HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusUpgradeRequired { // 426
		t.Fatalf("status = %d, want 426 Upgrade Required (drives HTTP fallback)", resp.StatusCode)
	}
}

// TestWebSocketRealtimeNot426 guards the footgun: a provider WebSocket with NO
// HTTP fallback (e.g. OpenAI Realtime /v1/realtime) must NOT be 426'd — that
// would break it. We assert the proxy does NOT reply 426 (it transparently
// relays instead; here the upstream dial fails so we get 502, but crucially not
// 426). The point is purely: /responses → 426, everything else → not 426.
func TestWebSocketRealtimeNot426(t *testing.T) {
	if wsHasHTTPFallback("/v1/realtime") {
		t.Fatal("/v1/realtime must NOT be treated as having an HTTP fallback")
	}
	if !wsHasHTTPFallback("/v1/responses") {
		t.Fatal("/v1/responses must be treated as having an HTTP fallback")
	}

	tmp := t.TempDir()
	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	const host = "api.openai.com"
	fmt.Fprintf(raw, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
	connBR := bufio.NewReader(raw)
	if r, err := http.ReadResponse(connBR, &http.Request{Method: "CONNECT"}); err != nil || r.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, r)
	}
	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	tlsConn := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: roots})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	fmt.Fprintf(tlsConn, "GET /v1/realtime HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), &http.Request{Method: "GET"})
	if err != nil {
		return // a relay/dial failure (no real upstream) is acceptable; the point is below
	}
	if resp.StatusCode == http.StatusUpgradeRequired {
		t.Fatalf("/v1/realtime got 426 — that breaks a no-fallback WS; must be relayed instead")
	}
}

func testResolution(t *testing.T, dir, instructionText string) resolver.Resolution {
	t.Helper()
	p := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(p, []byte(instructionText), 0o644); err != nil {
		t.Fatal(err)
	}
	return resolver.Resolution{Knowledge: []resolver.KnowledgeLayer{{
		Scope: resolver.ScopeGlobal, Label: "global", Path: p, Exists: true, Bytes: len(instructionText),
	}}}
}
