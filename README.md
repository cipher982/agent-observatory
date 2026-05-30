# Agent Context Observatory

See what your coding agents actually received.

Agent Context Observatory is a local-first macOS app for inspecting the context
assembled by tools like Codex, Claude, and Antigravity: instructions, skill
activation, tool schemas, transcript evidence, and live outbound LLM requests.

Today, agent context is mostly invisible. You find out it was wrong only after
the agent misses a rule, lacks a tool, or quietly uses stale instructions. This
app makes that state visible.

## What It Shows

- Recent local agent sessions from on-disk transcripts.
- Expected instructions, skills, and tools for each workspace.
- What was observed in transcripts.
- What was verified on the wire through an explicit local proxy.
- Drift: expected context that was missing.
- Conflicts: cases where transcript and wire evidence disagree.
- A realtime feed of agent requests as they leave the machine.

## Quick Start

Requirements:

- macOS 26+
- Xcode 26+
- XcodeGen
- Go 1.26+

Build and run the app:

```bash
make app-build
open /tmp/observatory-dd/Build/Products/Debug/Observatory.app
```

The app opens in **Demo** mode by default so the live feed is immediately useful.
Use the menu bar extra to switch Demo/Live mode, refresh sessions, show the main
window, or quit.

## Build Everything

```bash
make qa
```

This runs backend build, vet, unit tests, race tests, install lifecycle QA, and
the macOS app build.

## Release Artifact

```bash
make release
```

Artifacts are written to `dist/`:

- `Agent-Context-Observatory-0.1.0-macos.zip`
- `agents`
- `SHA256SUMS`

The local release build is ad-hoc signed. A public binary download should be
Developer ID signed and notarized before broad distribution.

## CLI

The app bundles the Go engine, but the backend is also usable directly:

```bash
cd backend
go run ./cmd/agents sessions --limit 20
go run ./cmd/agents context explain /path/to/project
go run ./cmd/agents monitor --demo
go run ./cmd/agents doctor wire
```

## Real Capture

There are two evidence levels:

- **Observed**: read from local CLI transcripts. Passive, no proxy required.
- **Verified**: captured from outbound LLM requests through an explicit local
  HTTPS proxy.

For real verified capture, install once:

```bash
agents install
agents status
```

After install, use Claude, Codex, and other agents normally. Newly launched
agents route through the local Observatory proxy automatically. No wrapper
command, managed launch, browser extension, or hosted account is required.

To remove the setup:

```bash
agents uninstall
```

Install sets a local launchd daemon, a local CA, and the process environment
needed by newly launched agents. Uninstall reverses the setup and is covered by
a looped fake-home QA harness.

## Security Model

This is a local app. The engine binds to `127.0.0.1`; there is no hosted service
and no cloud database.

Verified capture exists because HTTPS request bodies cannot be passively
inspected. Observatory creates its own local CA for the agent-to-proxy leg and
injects trust through agent process environment variables. It does not install
that CA into the macOS System keychain. The proxy's upstream connection to
OpenAI, Anthropic, Bedrock, and other providers uses the normal system trust
store.

Persisted capture state stores derived facts such as prompt length, marker
presence, endpoint, and tool names. Raw prompt bodies are not persisted.

## Architecture

```text
SwiftUI app
  -> bundled Go engine
      -> transcript discovery
      -> context resolver
      -> fact/evidence model
      -> localhost JSON API
      -> SSE live stream
      -> optional HTTPS proxy
```

The backend is the source of truth. The app, CLI, and API all render the same
fact-level model.

## Current Limitations

- macOS 26 and Xcode 26 are required for the Liquid Glass SwiftUI surface.
- Verified capture requires explicit proxy/trust setup.
- Antigravity transcript contents are discovery-only when stored in opaque `.pb`
  files.
- This release observes context. It does not yet manage canonical context
  upstream for every agent runtime.

## Development

```bash
make backend-qa
make app-build
make release
```

The release source of truth is `docs/specs/release-readiness.md`.
