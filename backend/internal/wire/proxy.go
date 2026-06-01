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
	"sync"
	"time"
)

// maxParseBody caps how much of an inspected request body we buffer in memory to
// parse. Real provider requests (even with tools/multimodal) fit comfortably;
// anything larger is forwarded unparsed so a local process can't grow the
// always-on daemon's memory without bound by POSTing a huge body to a provider.
const maxParseBody = 8 << 20 // 8 MiB

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
	// OnClientHandshakeError fires when a CLIENT (the agent) fails the TLS
	// handshake against our leaf — almost always "the agent doesn't trust our CA"
	// (UnknownIssuer). The daemon uses this to warn that capture is breaking an
	// agent rather than silently degrading it.
	OnClientHandshakeError func(host string, err error)

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
		if p.OnClientHandshakeError != nil {
			p.OnClientHandshakeError(host, err)
		}
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
	// Copy both directions; when EITHER leg ends, close BOTH conns so the other
	// copy unblocks instead of hanging on a half-open tunnel.
	var once sync.Once
	shutdown := func() { once.Do(func() { client.Close(); up.Close() }) }
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(up, client)
		shutdown()
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, up)
	shutdown()
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

		// WebSocket upgrade (RFC 6455): the bytes after the 101 are WS frames, not
		// HTTP, so we must switch to a transparent byte tunnel or we'd break the
		// agent (Codex's primary transport is wss://…/responses). We relay the
		// handshake + tunnel the frames; we do NOT yet parse the (masked +
		// permessage-deflate) frames, so a WS flow yields metadata only, not the
		// prompt/tool payload. See relayWebSocket.
		if isWebSocketUpgrade(req) {
			p.relayWebSocket(host, port, req, br, client)
			return
		}

		// Read a bounded prefix of the body so we can parse it. If the body fits
		// the cap we forward those exact bytes; if it's larger we forward the
		// prefix + the rest of the stream UNCHANGED and skip parsing, so a huge
		// request can't grow the daemon's memory without bound.
		var bodyBytes []byte
		var bodyRest io.Reader
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(io.LimitReader(req.Body, maxParseBody+1))
			if len(bodyBytes) > maxParseBody {
				bodyRest = req.Body // forward the remainder; do not buffer it
			} else {
				_ = req.Body.Close()
			}
		}

		if bodyRest == nil {
			if cap, ok := ParseBody(host, req.URL.Path, bodyBytes); ok {
				cap.Host = host
				cap.When = time.Now()
				p.logger.Printf("[wire] %s %s -> %s", host, req.URL.Path, cap.summary())
				if p.OnCapture != nil {
					p.OnCapture(cap)
				}
			}
		} else {
			p.logger.Printf("[wire] %s %s -> body over %d bytes; forwarded unparsed", host, req.URL.Path, maxParseBody)
		}

		resp, upstream, err := p.forwardStream(host, port, req, bodyBytes, bodyRest)
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

// isWebSocketUpgrade reports whether req is an RFC 6455 WebSocket opening
// handshake (Connection: Upgrade + Upgrade: websocket, case-insensitive).
func isWebSocketUpgrade(req *http.Request) bool {
	if !tokenListContains(req.Header.Get("Connection"), "upgrade") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket")
}

// tokenListContains reports whether a comma-separated header value contains tok
// (case-insensitive), e.g. Connection: "keep-alive, Upgrade".
func tokenListContains(header, tok string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), tok) {
			return true
		}
	}
	return false
}

// relayWebSocket handles a WebSocket flow on an inspected (TLS-terminated) host.
// It records a metadata-only capture of the opening request, replays the
// handshake to the real upstream over HTTP/1.1, and — once the upstream returns
// 101 Switching Protocols — relays the upgraded connection as an opaque
// bidirectional byte tunnel. The post-101 stream is WebSocket frames (masked,
// possibly permessage-deflate compressed); we deliberately do NOT parse them, so
// the agent keeps working even though we don't extract the prompt/tool payload.
//
// br is the buffered reader over the decrypted client conn — it may already hold
// bytes the client sent after the handshake, so we drain it (not the raw conn)
// into the upstream.
func (p *Proxy) relayWebSocket(host, port string, req *http.Request, br *bufio.Reader, client net.Conn) {
	// Metadata-only capture: we can't see the body/tools inside WS frames, but we
	// can record that this runtime opened a provider WS to this path.
	if p.OnCapture != nil {
		p.OnCapture(Capture{
			Host:     host,
			Endpoint: wsEndpoint(host, req.URL.Path),
			When:     time.Now(),
		})
	}
	p.logger.Printf("[wire] %s %s -> websocket upgrade (tunneled, metadata only)", host, req.URL.Path)

	// Dial upstream and replay the handshake byte-faithfully over HTTP/1.1.
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		writeBadGateway(client)
		return
	}
	upTLS := p.upstreamTLS.Clone()
	upTLS.ServerName = host
	if len(upTLS.NextProtos) == 0 {
		upTLS.NextProtos = []string{"http/1.1"} // RFC 6455 upgrade needs HTTP/1.1
	}
	up := tls.Client(rawConn, upTLS)
	if err := up.Handshake(); err != nil {
		rawConn.Close()
		writeBadGateway(client)
		return
	}
	defer up.Close()

	outReq := req.Clone(req.Context())
	outReq.URL.Scheme = "https"
	outReq.URL.Host = host
	outReq.Host = host
	outReq.RequestURI = ""
	if err := outReq.Write(up); err != nil {
		writeBadGateway(client)
		return
	}

	// Read the handshake response. Use a buffered reader; it may capture bytes the
	// server sent immediately after the 101 (the first WS frames).
	upBR := bufio.NewReader(up)
	resp, err := http.ReadResponse(upBR, outReq)
	if err != nil {
		writeBadGateway(client)
		return
	}
	// Relay the response headers to the client verbatim (the 101, or a non-101
	// rejection like 426/401 which the client must see).
	if err := resp.Write(client); err != nil {
		resp.Body.Close()
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return // not upgraded; nothing more to tunnel
	}

	// Switch to an opaque bidirectional tunnel. CRUCIAL: drain the buffered bytes
	// already pulled into br (client side) and upBR (server side) before raw
	// copying, or we'd drop the first frames.
	var once sync.Once
	shutdown := func() { once.Do(func() { client.Close(); up.Close() }) }
	done := make(chan struct{}, 1)
	go func() { // client -> upstream
		_, _ = io.Copy(up, br)
		shutdown()
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, upBR) // upstream -> client
	shutdown()
	<-done
}

// wsEndpoint gives a stable logical name for a provider WebSocket flow, e.g.
// "openai/responses (ws)" for OpenAI's /v1/responses WS endpoint.
func wsEndpoint(host, path string) string {
	switch {
	case strings.Contains(host, "openai") && strings.Contains(path, "responses"):
		return "openai/responses (ws)"
	case strings.Contains(host, "anthropic"):
		return "anthropic (ws)"
	default:
		return host + " (ws)"
	}
}

// forward replays the request to the real upstream without touching any signed
// material: the method, path, query, every header, and the body are forwarded
// unchanged (we only re-point the URL/Host to dial the real upstream, which the
// agent already set to the same value). So SigV4 — which signs the canonical
// request including headers and a body hash — still validates upstream; we never
// re-sign and hold no AWS credentials.
//
// The returned response body is still attached to the live upstream connection,
// so the caller can stream it straight back to the agent. The caller MUST close
// the returned net.Conn once the response has been relayed.
// forwardStream replays the request to the real upstream without touching any
// signed material: the method, path, query, every header, and the body are
// forwarded unchanged (we only re-point the URL/Host to dial the real upstream,
// which the agent already set to the same value). So SigV4 — which signs the
// canonical request including headers and a body hash — still validates upstream;
// we never re-sign and hold no AWS credentials.
//
// body is the buffered (parsed) prefix; rest, if non-nil, is the remainder of an
// oversized body that we forward without buffering. The returned response body is
// still attached to the live upstream connection so the caller can stream it back
// to the agent; the caller MUST close the returned net.Conn after relaying.
func (p *Proxy) forwardStream(host, port string, req *http.Request, body []byte, rest io.Reader) (*http.Response, net.Conn, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, nil, err
	}
	upTLS := p.upstreamTLS.Clone()
	upTLS.ServerName = host
	// We speak HTTP/1.1 to the decrypted client and replay over HTTP/1.1 upstream,
	// so advertise only http/1.1 in ALPN — otherwise an h2-negotiating upstream
	// would expect HTTP/2 framing we don't send. (h2-only clients are unsupported;
	// the mainstream provider SDKs accept http/1.1.)
	if len(upTLS.NextProtos) == 0 {
		upTLS.NextProtos = []string{"http/1.1"}
	}
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
	if rest != nil {
		// Oversized body: concatenate the buffered prefix with the unread rest and
		// preserve the original ContentLength (req still carries it).
		outReq.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), rest))
	} else {
		outReq.Body = io.NopCloser(bytes.NewReader(body))
		outReq.ContentLength = int64(len(body))
	}

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
	case strings.HasPrefix(h, "aws-external-anthropic.") && strings.HasSuffix(h, ".api.aws"):
		return true
	default:
		return false
	}
}

func (c Capture) summary() string {
	return fmt.Sprintf("system %d chars, %d tools",
		len([]rune(c.SystemPrompt)), len(c.ToolNames))
}
