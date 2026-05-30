package wire

import (
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
