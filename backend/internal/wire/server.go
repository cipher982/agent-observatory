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
// VERIFIED observations it captures, keyed by a session id supplied by the
// managed launcher (via correlation). Until per-request session correlation is
// wired, captures are stored under a single active session id.
type Server struct {
	proxy *Proxy
	ca    *CA
	ln    net.Listener
	srv   *http.Server
	log   *log.Logger

	mu          sync.Mutex
	sessionID   string
	captures    []Capture
	subscribers map[int]chan Capture
	nextSub     int
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
	return newServerWithCA(ca, logger), nil
}

func newServerWithCA(ca *CA, logger *log.Logger) *Server {
	s := &Server{ca: ca, log: logger, subscribers: map[int]chan Capture{}}
	p := NewProxy(ca, logger)
	p.OnCapture = s.record
	s.proxy = p
	return s
}

// Subscribe returns a channel that receives every future capture, plus an
// unsubscribe func. Buffered so a slow consumer never blocks the proxy.
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
		if c, ok := s.subscribers[id]; ok {
			close(c)
			delete(s.subscribers, id)
		}
	}
}

// SetUpstreamTLS overrides the upstream-dial TLS config (tests trust a local
// self-signed server; production uses system roots).
func (s *Server) SetUpstreamTLS(cfg *tls.Config) { s.proxy.SetUpstreamTLS(cfg) }

// SetSession sets the session id captures are attributed to (the managed launcher
// supplies the agent's session id once known).
func (s *Server) SetSession(id string) {
	s.mu.Lock()
	s.sessionID = id
	s.mu.Unlock()
}

// Inject pushes a capture through the same record/fan-out path real wire
// captures take (used by demo mode to populate the live feed with mock data).
func (s *Server) Inject(c Capture) { s.record(c) }

func (s *Server) record(c Capture) {
	s.mu.Lock()
	s.captures = append(s.captures, c)
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

// Observations converts captured wire requests into VERIFIED tool observations
// for the given runtime + session. Use ObservationsForResolution when resolved
// instruction expectations are available too.
func (s *Server) Observations(runtime, sessionID string) []fact.Observation {
	return s.ObservationsForResolution(runtime, sessionID, resolver.Resolution{})
}

// ObservationsForResolution converts captured wire requests into VERIFIED
// fact.Observations for the given runtime/session/resolved context. The wire is
// a COMPLETE-coverage source for instruction text when the request body was
// parsed in memory.
func (s *Server) ObservationsForResolution(runtime, sessionID string, res resolver.Resolution) []fact.Observation {
	s.mu.Lock()
	caps := make([]Capture, len(s.captures))
	copy(caps, s.captures)
	s.mu.Unlock()

	var out []fact.Observation
	for i, c := range caps {
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
