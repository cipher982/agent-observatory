package wire

import (
	"bufio"
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
	"testing"
	"time"
)

// TestNERelayContract proves the exact contract the macOS NETransparentProxy
// extension relies on: the extension is a thin L4 relay that, for an allowlisted
// flow, opens a raw TCP socket to the Go proxy, writes a literal
//
//	CONNECT host:port HTTP/1.1\r\nHost: host:port\r\n\r\n
//
// reads the "HTTP/1.1 200 Connection Established" line, then pumps the agent's
// TLS bytes through untouched. This test drives that byte sequence by hand
// (NOT via http.Transport, which hides CONNECT) so it mirrors the Swift relay
// pump precisely, and asserts the proxy still terminates TLS, parses the body,
// and forwards byte-identically upstream.
func TestNERelayContract(t *testing.T) {
	tmp := t.TempDir()

	const reqBody = `{"system":"sys via NE relay","tools":[{"name":"mcp__reviewer__ask"}],"messages":[]}`
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
	// The NE extension only relays allowlisted provider hosts; the Go proxy then
	// decides TLS termination via SetInspectHost. Here the target is the test
	// upstream, so inspect it.
	srv.SetInspectHost(func(string) bool { return true })
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})

	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// --- This is the NE relay's exact behavior, by hand ---

	// 1) Raw TCP to the Go proxy (what NWConnection to 127.0.0.1:proxyPort does).
	raw, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer raw.Close()

	// 2) Write the literal CONNECT request the Swift relay emits.
	target := upHost + ":" + upPort
	connect := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := raw.Write([]byte(connect)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	// 3) Read the proxy's CONNECT response status line.
	br := bufio.NewReader(raw)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if !containsStatus200(statusLine) {
		t.Fatalf("CONNECT response = %q, want 2xx Connection Established", statusLine)
	}
	// Drain the rest of the response headers up to the blank line.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("drain CONNECT headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// 4) Now the tunnel is open. Do TLS as the agent would, trusting our CA.
	//    NOTE: bufio may have buffered tunnel bytes, but for CONNECT the proxy
	//    sends only the status block before going transparent, so the raw conn
	//    is safe to hand to the TLS client (br read exactly the header block).
	caPEM, _ := os.ReadFile(srv.CAPath())
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	if br.Buffered() != 0 {
		t.Fatalf("unexpected %d buffered bytes after CONNECT header block", br.Buffered())
	}
	tlsConn := tls.Client(raw, &tls.Config{ServerName: upHost, RootCAs: roots})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("agent->proxy TLS handshake: %v", err)
	}

	// 5) Send the POST over the established tunnel.
	httpReq := fmt.Sprintf(
		"POST /v1/messages HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		upHost, len(reqBody), reqBody,
	)
	if _, err := tlsConn.Write([]byte(httpReq)); err != nil {
		t.Fatalf("write POST through tunnel: %v", err)
	}
	respBytes, _ := io.ReadAll(tlsConn)
	if !containsStatus200(string(respBytes)) {
		t.Fatalf("upstream response via relay = %q, want 200", truncateForLog(respBytes))
	}

	// --- Assertions: capture + fidelity, identical to the env-var proxy path ---
	if string(gotUpstreamBody) != reqBody {
		t.Errorf("upstream body mismatch via NE relay:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	caps := srv.Captures()
	if len(caps) == 0 {
		t.Fatalf("proxy captured nothing on the NE relay path")
	}
	c := caps[0]
	if len(c.ToolNames) != 1 || c.ToolNames[0] != "mcp__reviewer__ask" {
		t.Errorf("capture tools = %v, want [mcp__reviewer__ask]", c.ToolNames)
	}
	if c.AllText == "" {
		t.Errorf("capture missed assembled request text on NE relay path")
	}
}

func containsStatus200(s string) bool {
	return len(s) >= 12 && (s[9] == '2') // "HTTP/1.1 2xx"
}

func truncateForLog(b []byte) string {
	if len(b) > 80 {
		return string(b[:80]) + "..."
	}
	return string(b)
}
