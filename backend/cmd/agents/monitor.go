package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/observatory"
	"github.com/cipher982/agent-observatory/backend/internal/wire"
)

// runMonitor runs the realtime monitor: the JSON API + the live SSE stream + an
// always-on intercepting proxy. The GUI connects to /api/stream and shows
// in-flight LLM requests the instant they're captured. In the product path this
// is started by `agents install`; direct use is mainly diagnostic/development.
func runMonitor(args []string) int {
	var (
		apiPort   int
		proxyPort int
		limit     int
		caDir     string
		demo      bool
	)
	parseFlags("monitor", args, func(fs *flag.FlagSet) {
		fs.IntVar(&apiPort, "port", 7878, "API + SSE port")
		fs.IntVar(&proxyPort, "proxy-port", 7879, "intercepting proxy port")
		fs.IntVar(&limit, "limit", 50, "default session cap")
		fs.StringVar(&caDir, "ca-dir", "", "stable CA dir (ambient install); empty = ephemeral CA")
		fs.BoolVar(&demo, "demo", false, "inject synthetic wire captures for screenshots/demos")
	})

	var srv *wire.Server
	var err error
	if caDir != "" {
		// Ambient/daemon mode: reuse a stable CA so env-injected trust stays valid.
		srv, err = wire.NewServerStableCA(caDir, nil, time.Now())
	} else {
		var tmp string
		if tmp, err = os.MkdirTemp("", "observatory-ca-*"); err == nil {
			srv, err = wire.NewServer(tmp, nil, time.Now())
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy init: %v\n", err)
		return 1
	}
	proxyAddr, err := srv.Listen(proxyPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy listen: %v\n", err)
		return 1
	}
	defer srv.Close()

	// Live in-memory ring of recent captures for the stream's initial replay.
	live := newLiveBus(srv)

	if demo {
		go runDemoInjector(srv)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "mode": "monitor", "proxy": "http://" + proxyAddr, "caPath": srv.CAPath()})
	})
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if demo {
			writeJSON(w, observatory.DemoSessions(limit))
			return
		}
		views, err := observatory.LiveSessions(limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, views)
	})
	mux.HandleFunc("/api/explain", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			path, _ = os.Getwd()
		}
		res, err := observatory.ExplainPath(path)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
	})
	// The realtime stream: Server-Sent Events of live wire captures.
	mux.HandleFunc("/api/stream", live.handleSSE)
	// Proxy coordinates so the GUI can show install/trust state.
	mux.HandleFunc("/api/proxy", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"httpsProxy": "http://" + proxyAddr,
			"caPath":     srv.CAPath(),
		})
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", apiPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "api listen: %v\n", err)
		return 1
	}
	fmt.Printf("agents monitor:\n")
	fmt.Printf("  API + live stream : http://%s  (/api/sessions /api/stream)\n", ln.Addr())
	fmt.Printf("  intercepting proxy: http://%s\n", proxyAddr)
	fmt.Printf("  local CA          : %s\n", srv.CAPath())
	if caDir == "" {
		fmt.Printf("\nDiagnostic mode. For normal use, run `agents install` once and then use agents normally.\n")
	} else {
		fmt.Printf("\nInstalled mode. Newly launched agents should be captured automatically.\n")
	}

	httpSrv := &http.Server{Handler: withCORS(mux), ReadHeaderTimeout: 5 * time.Second}
	if err := httpSrv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// liveBus bridges wire captures to SSE clients.
type liveBus struct {
	srv *wire.Server
}

func newLiveBus(srv *wire.Server) *liveBus { return &liveBus{srv: srv} }

// streamEvent is the JSON shape pushed to the GUI per live request.
type streamEvent struct {
	Type        string   `json:"type"` // "capture"
	Host        string   `json:"host"`
	Endpoint    string   `json:"endpoint"`
	Runtime     string   `json:"runtime"`
	SystemChars int      `json:"systemChars"`
	Parsed      bool     `json:"parsed"`
	ToolCount   int      `json:"toolCount"`
	ToolNames   []string `json:"toolNames"`
	At          string   `json:"at"`
}

func (b *liveBus) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := b.srv.Subscribe()
	defer unsub()

	// Initial hello so the client knows the stream is live.
	fmt.Fprintf(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	ctx := r.Context()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case c, ok := <-ch:
			if !ok {
				return
			}
			ev := streamEvent{
				Type: "capture", Host: c.Host, Endpoint: c.Endpoint,
				Runtime: runtimeForHost(c.Host), SystemChars: len([]rune(c.SystemPrompt)),
				Parsed: true, ToolCount: len(c.ToolNames), ToolNames: c.ToolNames,
				At: time.Now().Format(time.RFC3339),
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: capture\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// runtimeForHost infers the runtime from the upstream host.
func runtimeForHost(host string) string {
	switch {
	case strings.Contains(host, "bedrock-runtime"), strings.Contains(host, "anthropic"):
		return "claude"
	case strings.Contains(host, "openai"):
		return "codex"
	default:
		return "unknown"
	}
}

// runDemoInjector pushes realistic, varied synthetic captures through the live
// path on a timer, so the GUI's live feed looks alive for screenshots/demos.
// Seeds a few immediately, then drips new ones in.
func runDemoInjector(srv *wire.Server) {
	type sample struct {
		host, endpoint string
		system         int
		tools          []string
	}
	samples := []sample{
		{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock/invoke", 5548,
			[]string{"Bash", "Edit", "Read", "Grep", "Glob", "Write", "mcp__review-agent__ask", "mcp__context-store__recall"}},
		{"api.openai.com", "openai/responses", 41833,
			[]string{"exec_command", "apply_patch", "update_plan", "mcp__docs__query", "mcp__search-hub__search"}},
		{"api.anthropic.com", "anthropic/messages", 6120,
			[]string{"str_replace", "bash", "mcp__drive__list_files", "mcp__tickets__list_items"}},
		{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock/invoke", 5212,
			[]string{"Read", "Edit", "Bash", "mcp__image-hub__generate_image"}},
		{"api.openai.com", "openai/responses", 38904,
			[]string{"exec_command", "apply_patch"}},
	}
	inject := func(i int) {
		s := samples[i%len(samples)]
		sys := make([]string, 0, 1)
		if s.system > 0 {
			sys = append(sys, repeatRune('x', s.system))
		}
		srv.Inject(wire.Capture{
			Host: s.host, Endpoint: s.endpoint,
			SystemPrompt: join(sys), AllText: join(sys),
			ToolNames: s.tools,
		})
	}
	// seed a couple immediately
	inject(0)
	inject(1)
	i := 2
	for {
		select {
		case <-time.After(3 * time.Second):
			inject(i)
			i++
		}
	}
}

func repeatRune(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}

func join(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s
	}
	return out
}
