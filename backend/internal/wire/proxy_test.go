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
		"API.ANTHROPIC.COM", // case-insensitive
		"api.anthropic.com.", // trailing dot
	}
	deny := []string{
		"example.com",
		"evil-amazonaws.com",
		"amazonaws.com.attacker.com",
		"s3.amazonaws.com",                  // AWS but not bedrock
		"aws-external-anthropic.evil.com",   // right prefix, wrong suffix
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

// TestWebSocketUpgradeIsTunneled proves the proxy doesn't break a WebSocket
// flow on an inspected host: it must relay the 101 handshake and then tunnel
// frames bidirectionally (Codex's primary transport is wss://…/responses). We
// stand up a real WS-ish upstream (101 + a server frame, echoes a client frame)
// and assert end-to-end delivery + a metadata capture.
func TestWebSocketUpgradeIsTunneled(t *testing.T) {
	tmp := t.TempDir()

	// Upstream TLS server that completes an RFC 6455-style upgrade by hand, then
	// echoes one post-upgrade message back over the hijacked conn.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			w.WriteHeader(400)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("upstream not hijackable")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		// Send the 101 + a "hello" payload, then echo whatever the client sends.
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nSERVER_HELLO")
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(append([]byte("ECHO:"), buf[:n]...))
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

	// Connect to the proxy, CONNECT to the upstream host:port, TLS as a client
	// trusting our CA, then send a raw WebSocket upgrade and exchange frames.
	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	fmt.Fprintf(raw, "CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n\r\n", upHost, upPort, upHost, upPort)
	connBR := bufio.NewReader(raw)
	connResp, err := http.ReadResponse(connBR, &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, connResp)
	}

	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	tlsConn := tls.Client(raw, &tls.Config{ServerName: upHost, RootCAs: roots})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	// Send the WS opening handshake.
	fmt.Fprintf(tlsConn, "GET /v1/responses HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", upHost)
	tlsBR := bufio.NewReader(tlsConn)
	resp, err := http.ReadResponse(tlsBR, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read 101: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	// Read the server's post-upgrade hello (already past the header terminator).
	hello := make([]byte, len("SERVER_HELLO"))
	if _, err := io.ReadFull(tlsBR, hello); err != nil {
		t.Fatalf("read server hello: %v", err)
	}
	if string(hello) != "SERVER_HELLO" {
		t.Errorf("server hello = %q, want SERVER_HELLO", hello)
	}
	// Send a client frame, expect the echo — proves the client->upstream leg.
	if _, err := tlsConn.Write([]byte("PING")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	echo := make([]byte, len("ECHO:PING"))
	if _, err := io.ReadFull(tlsBR, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != "ECHO:PING" {
		t.Errorf("echo = %q, want ECHO:PING", echo)
	}

	// A metadata-only capture must have been recorded for the WS open (the test
	// upstream host isn't a real provider, so the endpoint is the generic ws form).
	caps := srv.Captures()
	if len(caps) != 1 || !strings.HasSuffix(caps[0].Endpoint, "(ws)") {
		t.Fatalf("ws capture = %+v, want one *(ws) capture", caps)
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
