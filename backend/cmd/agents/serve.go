package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/observatory"
)

// runServe runs the localhost JSON API that frontends consume. It is the
// product's default mode: the SwiftUI app spawns `agents serve` as a helper and
// renders the JSON; the web dashboard and curl can hit the same endpoints.
//
// Endpoints:
//
//	GET /healthz                 -> {"ok":true,"version":...}
//	GET /api/sessions?limit=N    -> []observatory.SessionView
//	GET /api/explain?path=P      -> resolver.Resolution
func runServe(args []string) int {
	var (
		port  int
		limit int
	)
	parseFlags("serve", args, func(fs *flag.FlagSet) {
		fs.IntVar(&port, "port", 7878, "localhost port to listen on")
		fs.IntVar(&limit, "limit", 50, "default cap on sessions returned")
	})

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": "0.1.0", "ts": time.Now().UTC()})
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		n := limit
		if q := r.URL.Query().Get("limit"); q != "" {
			if parsed, err := parsePositive(q); err == nil {
				n = parsed
			}
		}
		views, err := observatory.LiveSessions(n)
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

	// Bind to loopback only — this is a personal, local engine, never exposed.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen on 127.0.0.1:%d: %v\n", port, err)
		return 1
	}
	fmt.Printf("agents serve: http://%s  (endpoints: /healthz /api/sessions /api/explain)\n", ln.Addr())

	srv := &http.Server{Handler: withCORS(mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// withCORS scopes cross-origin access to localhost origins only. The native app
// uses URLSession (not subject to CORS), so the only legitimate browser caller
// is a future local dashboard served from loopback. A wildcard ACAO would let
// ANY website the user visits read /api/sessions and /api/explain?path=... from
// the always-on daemon, so we echo back only localhost/127.0.0.1 origins.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); isLoopbackOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// isLoopbackOrigin reports whether an Origin header points at the local machine.
// Anything else (a real website) gets no ACAO header and is blocked by the
// browser same-origin policy.
func isLoopbackOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func parsePositive(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid limit %q", s)
	}
	return n, nil
}
