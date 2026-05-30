package wire

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
)

// TestNativeAnthropicPathThroughProxy proves the Claude-via-Anthropic path with
// the SAME interception mechanism as Bedrock: a TLS client (standing in for
// Claude Code's Node/Bun stack) POSTs an Anthropic /v1/messages body through the
// proxy. We assert capture + byte-identical forward + a VERIFIED observation.
//
// (The live Bedrock and Codex paths are proven against the real CLIs in the goal
// run; this covers the native-Anthropic host, which has no API key on this
// machine, using the identical proxy code path.)
func TestNativeAnthropicPathThroughProxy(t *testing.T) {
	tmp := t.TempDir()
	const reqBody = `{"system":[{"type":"text","text":"You are Claude. Run release smoke tests before launch."}],"tools":[{"name":"mcp__search-hub__query"},{"name":"mcp__issue-hub__list"}],"messages":[{"role":"user","content":"hi"}]}`

	var gotBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message"}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)
	upHost, upPort, _ := net.SplitHostPort(upURL.Host)

	srv, err := NewServer(tmp, log.New(io.Discard, "", 0), time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})
	addr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}, Timeout: 10 * time.Second}

	req, _ := http.NewRequest("POST", "https://"+upHost+":"+upPort+"/v1/messages", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "dummy-test-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("anthropic request through proxy: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if string(gotBody) != reqBody {
		t.Errorf("upstream did not receive byte-identical body")
	}
	caps := srv.Captures()
	if len(caps) != 1 || caps[0].Endpoint != "anthropic/messages" {
		t.Fatalf("expected 1 anthropic/messages capture, got %+v", caps)
	}
	if caps[0].AllText == "" {
		t.Errorf("capture should retain assembled text in memory")
	}
	if len(caps[0].ToolNames) != 2 {
		t.Errorf("tools = %v, want 2", caps[0].ToolNames)
	}
	// VERIFIED observation with canonicalized tool server names.
	obs := srv.Observations("claude", "sess-anthropic")
	var search bool
	for _, o := range obs {
		if o.Key.Kind == fact.ToolAvailable && o.Key.Name == "search_hub" && o.Level == fact.Verified {
			search = true
		}
	}
	if !search {
		t.Errorf("expected VERIFIED search_hub tool observation, got %+v", obs)
	}
}
