package wire

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/evidence"
	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
)

// Server runs the intercepting proxy on a loopback port and accumulates the
// VERIFIED observations it captures. Until per-request session correlation is
// wired, captures are stored under a single active session id.
type Server struct {
	proxy *Proxy
	ca    *CA
	ln    net.Listener
	srv   *http.Server
	log   *log.Logger

	mu              sync.Mutex
	sessionID       string
	captures        []Capture
	bypasses        []Bypass
	subscribers     map[int]chan Capture
	bypassSubs      map[int]chan Bypass
	nextSub         int
	nextBypassSub   int
	clientTLSFails  int // agent handshakes rejected (untrusted CA)
	lastTLSFailHost string
	pauseOnTLSFail  bool
}

// NewServer builds a proxy server with an ephemeral CA whose PEM is written under
// caDir (for the child's trust env).
func NewServer(caDir string, logger *log.Logger, now time.Time) (*Server, error) {
	ca, err := NewCA(caDir, now)
	if err != nil {
		return nil, err
	}
	return newServerWithCA(ca, logger), nil
}

// NewServerStableCA builds a proxy server with a STABLE CA persisted under caDir
// (reused across restarts). Used by the ambient install daemon.
func NewServerStableCA(caDir string, logger *log.Logger, now time.Time) (*Server, error) {
	ca, err := LoadOrCreateCA(caDir, now)
	if err != nil {
		return nil, err
	}
	s := newServerWithCA(ca, logger)
	s.pauseOnTLSFail = true
	// Stable/installed mode is reached from the NetworkExtension. Require the
	// v0.3 source-metadata marker before inspecting so an older/malformed relay
	// fails open as pass-through instead of MITMing unknown clients.
	s.proxy.capturePolicy.requireTransparentMetadata = true
	return s, nil
}

func newServerWithCA(ca *CA, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{ca: ca, log: logger, subscribers: map[int]chan Capture{}, bypassSubs: map[int]chan Bypass{}}
	p := NewProxy(ca, logger)
	p.OnCapture = s.record
	p.OnBypass = s.recordBypass
	p.OnClientHandshakeError = func(host string, err error) {
		s.mu.Lock()
		s.clientTLSFails++
		s.lastTLSFailHost = host
		pause := s.pauseOnTLSFail
		s.mu.Unlock()
		if pause {
			reason := fmt.Sprintf("paused after client TLS trust failure for %s at %s: %v", host, time.Now().Format(time.RFC3339), err)
			if perr := PauseCapture(reason); perr != nil {
				logger.Printf("[wire] capture pause marker failed: %v", perr)
			} else {
				logger.Printf("[wire] capture paused: %s", reason)
			}
		}
	}
	s.proxy = p
	return s
}

// ClientTLSFailures reports how many times an agent rejected our leaf cert (the
// "agent doesn't trust the CA" case) and the most recent host. The daemon
// surfaces this so the UI can explain why capture was paused.
func (s *Server) ClientTLSFailures() (count int, lastHost string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientTLSFails, s.lastTLSFailHost
}

// Subscribe returns a channel that receives every future capture, plus an
// unsubscribe func. Buffered so a slow consumer never blocks the proxy.
//
// Unsubscribe deletes the channel from the registry but deliberately does NOT
// close it: record() sends without holding the lock, so closing here would race
// a concurrent send and panic the always-on daemon. The channel is simply
// dropped and garbage-collected once the SSE handler returns.
func (s *Server) Subscribe() (<-chan Capture, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan Capture, 64)
	s.subscribers[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subscribers, id)
	}
}

// SubscribeBypasses returns future provider flows that were safely tunneled
// instead of TLS-inspected.
func (s *Server) SubscribeBypasses() (<-chan Bypass, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextBypassSub
	s.nextBypassSub++
	ch := make(chan Bypass, 64)
	s.bypassSubs[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.bypassSubs, id)
	}
}

// SetUpstreamTLS overrides the upstream-dial TLS config (tests trust a local
// self-signed server; production uses system roots).
func (s *Server) SetUpstreamTLS(cfg *tls.Config) { s.proxy.SetUpstreamTLS(cfg) }

// SetInspectHost overrides the host inspection policy. Tests use this to inspect
// localhost; production defaults to known LLM provider endpoints only.
func (s *Server) SetInspectHost(fn func(host string) bool) { s.proxy.SetInspectHost(fn) }

// SetCapturePolicy overrides the transparent-capture source/trust gate. Tests
// use this to prove supported, stale, and unknown-source behavior deterministically.
func (s *Server) SetCapturePolicy(policy *CapturePolicy) { s.proxy.SetCapturePolicy(policy) }

// SetSession sets the session id captures are attributed to.
func (s *Server) SetSession(id string) {
	s.mu.Lock()
	s.sessionID = id
	s.mu.Unlock()
}

// Inject pushes a capture through the same record/fan-out path real wire
// captures take (used by demo mode to populate the live feed with mock data).
func (s *Server) Inject(c Capture) { s.record(c) }

// maxCaptures bounds the in-memory capture history. The daemon is always-on, so
// an unbounded slice would grow without limit across a work session (each entry
// retains the full assembled prompt text). We keep the most recent N.
const maxCaptures = 500

const maxBypasses = 500

func (s *Server) record(c Capture) {
	s.mu.Lock()
	s.captures = append(s.captures, c)
	if len(s.captures) > maxCaptures {
		// Drop oldest; copy to a fresh slice so the backing array can shrink.
		s.captures = append([]Capture(nil), s.captures[len(s.captures)-maxCaptures:]...)
	}
	subs := make([]chan Capture, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	// Fan out to live subscribers without holding the lock; drop on a full buffer.
	for _, ch := range subs {
		select {
		case ch <- c:
		default:
		}
	}
}

func (s *Server) recordBypass(b Bypass) {
	if b.Runtime == "" {
		b.Runtime = runtimeForProviderHost(b.Host)
	}
	if b.When.IsZero() {
		b.When = time.Now()
	}
	s.mu.Lock()
	s.bypasses = append(s.bypasses, b)
	if len(s.bypasses) > maxBypasses {
		s.bypasses = append([]Bypass(nil), s.bypasses[len(s.bypasses)-maxBypasses:]...)
	}
	subs := make([]chan Bypass, 0, len(s.bypassSubs))
	for _, ch := range s.bypassSubs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- b:
		default:
		}
	}
}

// CAPath is the on-disk CA cert path for injecting into the child's trust env.
func (s *Server) CAPath() string { return s.ca.PEMPath() }

// Listen starts the proxy on 127.0.0.1:<port> (port 0 = auto). Returns the bound
// address. Serve runs in the background.
func (s *Server) Listen(port int) (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.proxy}
	go func() { _ = s.srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

// Close stops the proxy.
func (s *Server) Close() error {
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// Captures returns a copy of the captures collected so far.
func (s *Server) Captures() []Capture {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Capture, len(s.captures))
	copy(out, s.captures)
	return out
}

// Bypasses returns a copy of recent provider flows that were intentionally not
// full-captured.
func (s *Server) Bypasses() []Bypass {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bypass, len(s.bypasses))
	copy(out, s.bypasses)
	return out
}

func (s *Server) BypassStats() (total int, last Bypass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total = len(s.bypasses)
	if total > 0 {
		last = s.bypasses[total-1]
	}
	return total, last
}

// Observations converts captured wire requests into VERIFIED tool observations
// for the given runtime + session. Use ObservationsForResolution when resolved
// instruction expectations are available too.
func (s *Server) Observations(runtime, sessionID string) []fact.Observation {
	return s.ObservationsForResolution(runtime, sessionID, resolver.Resolution{})
}

// ObservationsForRuntime is ObservationsForResolution restricted to captures
// whose upstream host maps to runtime (via hostRuntime), so captures are never
// cross-attributed across runtimes (e.g. a Codex/OpenAI capture under a Claude
// session). hostRuntime maps an upstream host to a runtime label.
func (s *Server) ObservationsForRuntime(runtime, sessionID string, res resolver.Resolution, hostRuntime func(host string) string) []fact.Observation {
	var keep func(Capture) bool
	if hostRuntime != nil {
		keep = func(c Capture) bool { return hostRuntime(c.Host) == runtime }
	}
	return s.observations(runtime, sessionID, res, keep)
}

// ObservationsForResolution converts captured wire requests into VERIFIED
// fact.Observations for the given runtime/session/resolved context. The wire is
// a COMPLETE-coverage source for instruction text when the request body was
// parsed in memory.
func (s *Server) ObservationsForResolution(runtime, sessionID string, res resolver.Resolution) []fact.Observation {
	return s.observations(runtime, sessionID, res, nil)
}

// observations is the shared core: one snapshot of the capture ring, optionally
// filtered by keep. Taking a single snapshot is what keeps request ids stable —
// the ring can rotate concurrently, but we index into the copy we hold.
func (s *Server) observations(runtime, sessionID string, res resolver.Resolution, keep func(Capture) bool) []fact.Observation {
	s.mu.Lock()
	caps := make([]Capture, len(s.captures))
	copy(caps, s.captures)
	s.mu.Unlock()

	var out []fact.Observation
	for i, c := range caps {
		if keep != nil && !keep(c) {
			continue
		}
		ep := fact.Epoch{SessionID: sessionID, RequestID: fmt.Sprintf("wire-%d", i)}
		out = append(out, evidence.ObserveInstructionText("wire", fact.Verified, runtime, ep, c.AllText, res, true)...)
		// Tools: each offered tool is a Present, Complete-coverage VERIFIED fact.
		for _, raw := range c.ToolNames {
			server := evidence.CanonTool(canonServer(raw))
			if server == "" {
				continue
			}
			out = append(out, fact.Observation{
				Key:      fact.FactKey{Kind: fact.ToolAvailable, Runtime: runtime, Name: server},
				Polarity: fact.Present, Level: fact.Verified,
				Source: "wire", Coverage: fact.CoverageComplete, Match: fact.MatchToolName, Epoch: ep,
			})
		}
	}
	return out
}

// canonServer extracts the mcp server segment from a raw tool name (mcp__x__y),
// else "" for builtins.
func canonServer(raw string) string {
	const p = "mcp__"
	if len(raw) <= len(p) || raw[:len(p)] != p {
		return ""
	}
	rest := raw[len(p):]
	for i := 0; i+1 < len(rest); i++ {
		if rest[i] == '_' && rest[i+1] == '_' {
			return rest[:i]
		}
	}
	return ""
}
