package wire

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Capture is one observed outbound LLM request (the VERIFIED evidence).
type Capture struct {
	Host         string
	Endpoint     string   // logical kind, e.g. "anthropic/messages", "bedrock/invoke"
	SystemPrompt string   // assembled system/instructions text
	AllText      string   // assembled system + user text, in memory only
	ToolNames    []string // tool/function names offered in the request
	When         time.Time
}

// Proxy is an HTTPS-intercepting forward proxy. Provider flows reach it via the
// NetworkExtension relay (or HTTPS_PROXY in the dev `agents run` path) as an
// HTTP CONNECT. For allowlisted hosts it terminates TLS using a per-host leaf
// from the CA, parses the LLM request body, forwards it BYTE-IDENTICAL upstream
// (preserving SigV4), and invokes OnCapture for each parsed request. Non-
// allowlisted CONNECT targets are tunneled opaquely without TLS termination.
type Proxy struct {
	ca        *CA
	logger    *log.Logger
	OnCapture func(Capture)

	upstreamTLS *tls.Config
	inspectHost func(host string) bool
}

// NewProxy builds an intercepting proxy backed by the given CA.
func NewProxy(ca *CA, logger *log.Logger) *Proxy {
	if logger == nil {
		logger = log.Default()
	}
	return &Proxy{
		ca:          ca,
		logger:      logger,
		upstreamTLS: &tls.Config{MinVersion: tls.VersionTLS12},
		inspectHost: defaultInspectHost,
	}
}

// SetUpstreamTLS overrides the TLS config used to dial real upstreams (default
// uses the system root pool). Tests use this to trust a local self-signed server.
func (p *Proxy) SetUpstreamTLS(cfg *tls.Config) { p.upstreamTLS = cfg }

// SetInspectHost overrides which CONNECT hosts are locally TLS-inspected. The
// default inspects known LLM provider endpoints and tunnels unrelated HTTPS
// traffic without terminating TLS.
func (p *Proxy) SetInspectHost(fn func(host string) bool) {
	if fn == nil {
		p.inspectHost = defaultInspectHost
		return
	}
	p.inspectHost = fn
}

// ServeHTTP handles CONNECT (the only method agents use for HTTPS upstreams).
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "only CONNECT supported", http.StatusMethodNotAllowed)
		return
	}
	p.handleConnect(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Hostname()
	port := r.URL.Port()
	if host == "" {
		host = stripPort(r.Host)
		if _, pp, err := net.SplitHostPort(r.Host); err == nil {
			port = pp
		}
	}
	if port == "" {
		port = "443"
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		p.logger.Printf("[wire] hijack: %v", err)
		return
	}
	defer clientConn.Close()

	if !p.inspectHost(host) {
		p.tunnel(host, port, clientConn)
		return
	}

	// Tell the client the tunnel is established, then do TLS as the server using
	// a leaf cert for the requested host.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	leaf, err := p.ca.LeafFor(host)
	if err != nil {
		p.logger.Printf("[wire] leaf for %s: %v", host, err)
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{Certificates: []tls.Certificate{*leaf}})
	if err := tlsConn.Handshake(); err != nil {
		p.logger.Printf("[wire] tls handshake %s: %v", host, err)
		return
	}
	defer tlsConn.Close()

	// Read HTTP requests off the decrypted client connection, capture, forward.
	p.pump(host, port, tlsConn)
}

func (p *Proxy) tunnel(host, port string, client net.Conn) {
	upstream := &net.Dialer{Timeout: 15 * time.Second}
	up, err := upstream.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		p.logger.Printf("[wire] tunnel dial %s:%s: %v", host, port, err)
		writeBadGateway(client)
		return
	}
	defer up.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(up, client)
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, up)
	<-done
}

// pump reads requests from the (decrypted) client connection and proxies each to
// the real upstream over a fresh TLS connection, forwarding bytes faithfully.
func (p *Proxy) pump(host, port string, client *tls.Conn) {
	br := bufio.NewReader(client)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				p.logger.Printf("[wire] read request %s: %v", host, err)
			}
			return
		}

		// Buffer body so we can parse AND forward it unchanged.
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
			_ = req.Body.Close()
		}

		if cap, ok := ParseBody(host, req.URL.Path, bodyBytes); ok {
			cap.Host = host
			cap.When = time.Now()
			p.logger.Printf("[wire] %s %s -> %s", host, req.URL.Path, cap.summary())
			if p.OnCapture != nil {
				p.OnCapture(cap)
			}
		}

		resp, upstream, err := p.forward(host, port, req, bodyBytes)
		if err != nil {
			p.logger.Printf("[wire] forward %s%s: %v", host, req.URL.Path, err)
			writeBadGateway(client)
			return
		}
		// Relay the upstream response back to the client verbatim. resp.Write
		// streams the body as it arrives off the still-open upstream connection,
		// so provider streaming (SSE / stream:true) reaches the agent token by
		// token instead of being buffered until generation completes.
		writeErr := resp.Write(client)
		resp.Body.Close()
		upstream.Close()
		if writeErr != nil || req.Close || resp.Close {
			return
		}
	}
}

// forward sends the request to the real upstream BYTE-IDENTICAL: same method,
// path, query, headers, and body. This preserves SigV4 (and any other signed
// material) so Bedrock accepts the original signature without re-signing.
//
// The returned response body is still attached to the live upstream connection,
// so the caller can stream it straight back to the agent. The caller MUST close
// the returned net.Conn once the response has been relayed.
func (p *Proxy) forward(host, port string, req *http.Request, body []byte) (*http.Response, net.Conn, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, nil, err
	}
	upTLS := p.upstreamTLS.Clone()
	upTLS.ServerName = host
	tlsConn := tls.Client(rawConn, upTLS)
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		return nil, nil, fmt.Errorf("upstream tls handshake: %w", err)
	}

	// Reconstruct the exact outbound request. We do NOT mutate signed headers.
	outReq := req.Clone(req.Context())
	outReq.URL.Scheme = "https"
	outReq.URL.Host = host
	outReq.Host = host
	outReq.RequestURI = ""
	outReq.Body = io.NopCloser(bytes.NewReader(body))
	outReq.ContentLength = int64(len(body))

	if err := outReq.Write(tlsConn); err != nil {
		tlsConn.Close()
		return nil, nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), outReq)
	if err != nil {
		tlsConn.Close()
		return nil, nil, err
	}
	return resp, tlsConn, nil
}

func writeBadGateway(w io.Writer) {
	_, _ = io.WriteString(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func defaultInspectHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	switch {
	case h == "api.openai.com", h == "api.anthropic.com":
		return true
	case strings.Contains(h, "bedrock-runtime.") && strings.HasSuffix(h, ".amazonaws.com"):
		return true
	case strings.HasPrefix(h, "aws-external-anthropic."):
		return true
	default:
		return false
	}
}

func (c Capture) summary() string {
	return fmt.Sprintf("system %d chars, %d tools",
		len([]rune(c.SystemPrompt)), len(c.ToolNames))
}
