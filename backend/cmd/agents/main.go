// Command agents is the headless engine of the Agent Context Observatory.
//
// It discovers running coding-agent sessions from on-disk CLI transcripts,
// resolves the effective agent context for each (scope + activation), and
// verifies what was actually assembled (the OBSERVED tier with witness marks).
//
// It exposes that as both a CLI and a localhost JSON API. The API is the single
// source of truth that every frontend renders — the SwiftUI macOS app, the CLI
// table, and the legacy web dashboard are all just consumers.
//
// Subcommands:
//
//	agents serve [--port N] [--limit N]   run the localhost JSON API (default)
//	agents sessions [--json] [--limit N]  print live sessions + verification
//	agents context explain [path] [--json]  print resolved context for a path
//	agents version
package main

import (
	"fmt"
	"os"
)

func main() {
	// Fold persisted wire captures into the fact pipeline as VERIFIED evidence
	// (no-op when none exist).
	installWireObservations()

	args := os.Args[1:]
	sub := "serve" // headless API is the default — that's the product's job
	if len(args) > 0 {
		switch first := args[0]; {
		case first == "serve", first == "sessions", first == "context", first == "run", first == "doctor", first == "monitor", first == "install", first == "uninstall", first == "status":
			sub = first
			args = args[1:]
		case first == "version", first == "-v", first == "--version":
			fmt.Println("agents-observatory 0.1.0")
			return
		case first == "help", first == "-h", first == "--help":
			usage()
			return
		case len(first) > 0 && first[0] != '-':
			fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", first)
			usage()
			os.Exit(2)
		}
	}

	switch sub {
	case "serve":
		os.Exit(runServe(args))
	case "sessions":
		os.Exit(runSessions(args))
	case "context":
		os.Exit(runContext(args))
	case "run":
		os.Exit(runRun(args))
	case "doctor":
		os.Exit(runDoctor(args))
	case "monitor":
		os.Exit(runMonitor(args))
	case "install":
		os.Exit(runInstall(args))
	case "uninstall":
		os.Exit(runUninstall(args))
	case "status":
		os.Exit(runStatus(args))
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agents — Agent Context Observatory engine

Usage:
  agents install                           ambient setup: daemon + CA + global env
  agents uninstall                         fully reverse the install
  agents status                            show install state
  agents monitor [--port N]                always-on proxy + API + live SSE stream
  agents serve [--port N] [--limit N]      localhost JSON API
  agents sessions [--json] [--limit N]     live sessions + evidence marks
  agents context explain [path] [--json]   resolved context for a path
  agents doctor wire                       per-runtime wire-capability report

After 'agents install', use your agents normally. Newly launched agents are
captured automatically — no wrapper or managed launch.
The SwiftUI app and CLI all consume the same engine.
`)
}
