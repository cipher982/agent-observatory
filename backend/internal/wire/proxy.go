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
	"time"
)

// Capture is one observed outbound LLM request (the VERIFIED evidence).
type Capture struct {
	Host         string
	Endpoint     string   // logical kind, e.g. "anthropic/messages", "bedrock/invoke"
	SystemPrompt string   // assembled system/instructions text
	AgentsMarker bool     // doctrine marker present anywhere in the request
	MarkerSlot   string   // "system" | "user" | ""
	ToolNames    []string // tool/function names offered in the request
	When         time.Time
}

// Proxy is an HTTPS-intercepting forward proxy. Agents reach it via HTTPS_PROXY;
// it terminates TLS using a per-host leaf from the ephemeral CA, parses the LLM
// request body, forwards it BYTE-IDENTICAL upstream (preserving SigV4), and
// invokes OnCapture for each parsed request.
type Proxy struct {
	ca        *CA
	logger    *log.Logger
	OnCapture func(Capture)

	upstreamTLS *tls.Config
}

// NewProxy builds an intercepting proxy backed by the given CA.
func NewProxy(ca *CA, logger *log.Logger) *Proxy {
	if logger == nil {
		logger = log.Default()
	}
	return &Proxy{ca: ca, logger: logger, upstreamTLS: &tls.Config{MinVersion: tls.VersionTLS12}}
}

// SetUpstreamTLS overrides the TLS config used to dial real upstreams (default
// uses the system root pool). Tests use this to trust a local self-signed server.
func (p *Proxy) SetUpstreamTLS(cfg *tls.Config) { p.upstreamTLS = cfg }

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

		resp, err := p.forward(host, port, req, bodyBytes)
		if err != nil {
			p.logger.Printf("[wire] forward %s%s: %v", host, req.URL.Path, err)
			writeBadGateway(client)
			return
		}
		// Relay the upstream response back to the client verbatim.
		if err := resp.Write(client); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()
		if req.Close || resp.Close {
			return
		}
	}
}

// forward sends the request to the real upstream, BYTE-IDENTICAL: same method,
// path, query, headers, and body. This preserves SigV4 (and any other signed
// material) so Bedrock accepts the original signature without re-signing.
func (p *Proxy) forward(host, port string, req *http.Request, body []byte) (*http.Response, error) {
	upstream := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := upstream.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	upTLS := p.upstreamTLS.Clone()
	upTLS.ServerName = host
	tlsConn := tls.Client(rawConn, upTLS)
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("upstream tls handshake: %w", err)
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
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), outReq)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	// Buffer the body fully so we can close the upstream conn and still relay it.
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tlsConn.Close()
	resp.Body = io.NopCloser(bytes.NewReader(rb))
	resp.ContentLength = int64(len(rb))
	return resp, nil
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

func (c Capture) summary() string {
	loc := ""
	if c.AgentsMarker {
		loc = " in " + c.MarkerSlot
	}
	return fmt.Sprintf("system %d chars (AGENTS: %v%s), %d tools",
		len([]rune(c.SystemPrompt)), c.AgentsMarker, loc, len(c.ToolNames))
}
