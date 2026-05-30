package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/wire"
)

// runRun implements `observatory run <claude|codex|antigravity> [args...]`.
// It owns the launch: starts the intercepting proxy with an ephemeral CA, sets
// the child's proxy + scoped-trust env, execs the real CLI, and on exit reports
// the VERIFIED facts captured from the wire. This is the OPT-IN path to the
// VERIFIED tier — never passive.
func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var port int
	var keepCA bool
	fs.IntVar(&port, "port", 0, "proxy port (0 = auto)")
	fs.BoolVar(&keepCA, "keep-ca", false, "do not delete the ephemeral CA dir on exit")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agents run <claude|codex|antigravity> [args...]")
		return 2
	}
	runtime := rest[0]
	cliArgs := rest[1:]

	bin, err := exec.LookPath(runtime)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find %q on PATH: %v\n", runtime, err)
		return 2
	}

	// Ephemeral CA dir.
	caDir, err := os.MkdirTemp("", "observatory-ca-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mktemp: %v\n", err)
		return 1
	}
	if !keepCA {
		defer os.RemoveAll(caDir)
	}

	srv, err := wire.NewServer(caDir, nil, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy init: %v\n", err)
		return 1
	}
	addr, err := srv.Listen(port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy listen: %v\n", err)
		return 1
	}
	defer srv.Close()
	proxyURL := "http://" + addr
	caPath := srv.CAPath()

	fmt.Printf("observatory: intercepting %s via %s (CA: %s)\n", runtime, proxyURL, caPath)

	// Build the child env: route through the proxy + trust the ephemeral CA.
	// Trust env varies by runtime stack:
	//   - Node/Bun (claude, antigravity): NODE_EXTRA_CA_CERTS
	//   - Rust/rustls (codex): SSL_CERT_FILE
	//   - AWS SDK (bedrock path inside claude): AWS_CA_BUNDLE
	// We set all three; harmless where unused.
	env := append(os.Environ(),
		"HTTPS_PROXY="+proxyURL,
		"https_proxy="+proxyURL,
		"HTTP_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"NODE_EXTRA_CA_CERTS="+caPath,
		"SSL_CERT_FILE="+caPath,
		"AWS_CA_BUNDLE="+caPath,
		"NODE_OPTIONS=--use-bundled-ca", // ensure NODE_EXTRA_CA_CERTS is honored
	)

	cmd := exec.Command(bin, cliArgs...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Forward signals to the child.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "launch %s: %v\n", runtime, err)
		return 1
	}
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()
	_ = cmd.Wait()
	signal.Stop(sig)

	// Report what we verified on the wire.
	caps := srv.Captures()
	fmt.Fprintf(os.Stderr, "\nobservatory: captured %d outbound LLM request(s) for %s\n", len(caps), runtime)
	for i, c := range caps {
		fmt.Fprintf(os.Stderr, "  [%d] %s host=%s system=%dch tools=%d\n",
			i, c.Endpoint, c.Host, len([]rune(c.SystemPrompt)), len(c.ToolNames))
	}
	// Persist captures so `agents sessions` can fold them in as VERIFIED facts.
	if len(caps) > 0 {
		if err := persistCaptures(runtime, caps); err != nil {
			fmt.Fprintf(os.Stderr, "  (warning: could not persist captures: %v)\n", err)
		}
	}
	return 0
}

// persistCaptures writes captures to a per-runtime file under the observatory
// state dir so the verifier can surface them as VERIFIED evidence.
func persistCaptures(runtime string, caps []wire.Capture) error {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return wire.WriteCaptures(filepath.Join(dir, "wire-"+runtime+".json"), caps)
}

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-observatory")
}
