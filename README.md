# Agent Context Observatory

Makes agent context **observable**: which coding agents are running, what
context/skills/tools each one *should* have (resolved), and what it *actually*
assembled — confirmed at three escalating confidence tiers, with at-a-glance
witness marks. Closes the gulf of evaluation: today agent context is implicit and
you only notice it's wrong when an agent misbehaves.

## Architecture: headless engine + native frontend

```
┌──────────────────────────────┐        ┌──────────────────────────────┐
│ backend/  (Go) — headless     │ HTTP   │ app/  (SwiftUI) — native      │
│ resolver · transcript ·       │ JSON   │ macOS 26 Liquid Glass UI      │
│ evidence · fact · wire        │ ─────▶ │ zero business logic           │
│ `agents serve --port 7878`    │ :7878  │ renders fact-level marks      │
└──────────────────────────────┘        └──────────────────────────────┘
        └── same API also feeds the CLI (`agents sessions`) and curl
```

The Go engine is the single source of truth. The SwiftUI app, the CLI, and any
other consumer render the same JSON. Concerns are separated: presentation polish
never touches resolution/verification logic.

## The fact-level evidence model

Confidence is a property of each **fact**, not a session. The resolver's
expectation is the *claim under test*, not evidence:

```
EXPECTED   resolver says it SHOULD be present     (the claim — NOT evidence)
OBSERVED   the CLI's transcript recorded it         (passive, every runtime)
VERIFIED   captured on the wire, can't be faked      (managed launch, opt-in)
```

Each fact carries its **coverage** (complete | positive-only | heuristic | none),
so a source that can only prove *presence* (Codex tools = invoked-only) never
produces false "missing" drift. **CONFLICT** fires only when two complete-coverage
sources disagree for the same request — the silent-regression alarm.

## VERIFIED tier — working MITM for all three runtimes

A TLS-terminating proxy (reached via `HTTPS_PROXY`, trusted per-launch via a
throwaway CA — never the System keychain) reads the assembled system prompt +
tool schema from the outbound request, then forwards it **byte-identical** so the
agent keeps working. Proven live on this machine:

| runtime | transport | result |
|---|---|---|
| **Codex** | Rust/rustls → `api.openai.com/v1/responses` | ✅ 41KB prompt, 22 tools |
| **Claude (Bedrock)** | Node/Bun → `bedrock-runtime.*/model/.../invoke` | ✅ byte-identical forward preserves SigV4 (AWS accepts original signature, no re-signing, no creds in proxy) — 11 tools |
| **Claude (Anthropic)** | Node/Bun → `api.anthropic.com/v1/messages` | ✅ identical mechanism (proxy test) |

Antigravity (the Gemini-CLI successor) stores opaque/encrypted `.pb` transcripts,
so it's **discovery-only** — sessions are surfaced, but context facts honestly
report `coverage: none` rather than faking marks.

## Backend (Go)

```bash
cd backend
make qa                                    # build + vet + tests + race
go run ./cmd/agents sessions --limit 20    # live sessions + fact marks
go run ./cmd/agents context explain ~/git/zerg
go run ./cmd/agents doctor wire            # per-runtime wire capability report
go run ./cmd/agents run codex exec "..."   # managed launch → VERIFIED capture
go run ./cmd/agents serve --port 7878      # localhost JSON API for the app
```

Endpoints: `GET /healthz`, `GET /api/sessions?limit=N`, `GET /api/explain?path=P`.

`agents run <runtime>` is the OPT-IN path to VERIFIED: it owns the launch, sets up
the proxy + scoped CA trust, correlates the capture, and persists only *derived*
facts (length, marker, tool names) — never raw prompt bodies.

## One-command ambient install (zero per-launch friction)

```bash
agents install      # → start always-on proxy daemon (launchd) + stable CA
                    #   + set HTTPS_PROXY + NODE_EXTRA_CA_CERTS + SSL_CERT_FILE
                    #   + AWS_CA_BUNDLE globally (~/.zshenv + launchctl setenv)
agents status       # show what's installed
agents uninstall    # fully reverse it — system restored exactly
```

After `install`, **every newly-launched agent is auto-captured at the VERIFIED
tier with no wrapper** — a fresh shell inherits the proxy + CA-trust env, and the
launchd daemon is always listening. Proven on a real machine: a clean
`zsh -lic 'claude -p ...'` (no wrapper, no manual env) was captured live as
`bedrock-runtime.../invoke-with-response-stream → 5548 chars, 11 tools`; a
subsequent `uninstall` left `~/.zshenv`, launchd, ports, and CA with **zero
residue**.

Properties (all covered by `make qa`'s looped install harness):
- **Idempotent** — re-installing replaces one fenced `~/.zshenv` block, never duplicates.
- **Reversible** — uninstall restores the profile byte-for-byte (sentinel-fenced block), unloads the daemon, unsets env, deletes the CA.
- **Partial-state safe** — uninstall cleans up whatever exists.
- **Trust stays scoped** — CA is trusted via env vars, never the System keychain.
- Already-running shells aren't captured until restarted (expected).

The installer is fully root-configurable (`internal/install.Target`), so the QA
harness runs install→verify→uninstall→assert-clean in throwaway fake-HOME roots
with a stubbed launchctl — never touching the real shell.

## Realtime monitor + live GUI

`agents monitor` runs the JSON API, an always-on intercepting proxy, AND a
Server-Sent-Events stream (`/api/stream`) of in-flight LLM requests. The native
app launches it and renders a **live activity feed**: each outbound request blooms
in as a Liquid Glass card the instant it crosses the wire (`GlassEffectContainer`
+ `.materialize` transition), over an animated `MeshGradient` hero extended under
the chrome via `backgroundExtensionEffect`, with a breathing LIVE heartbeat,
`scrollEdgeEffectStyle`, and `safeAreaBar` chrome. Proven live: agent fires →
wire capture → SSE → card materializes. Screenshots: `docs/screenshots/v2-live-*`.

```bash
go run ./cmd/agents monitor   # API+stream :7878, proxy :7879
# then launch any agent through the printed HTTPS_PROXY + CA env, or `agents run`
```

## Native app (SwiftUI, macOS 26)

Requires **Xcode 26+** and **macOS 26** (Liquid Glass APIs).

```bash
cd app
xcodegen generate
open Observatory.xcodeproj          # ⌘R
# or headless:
xcodebuild -project Observatory.xcodeproj -scheme Observatory \
  -configuration Debug -derivedDataPath /tmp/obs-dd build
open /tmp/obs-dd/Build/Products/Debug/Observatory.app
```

The app bundles the Go engine into `Observatory.app/Contents/Resources/agents`,
spawns it on launch, polls `/api/sessions`, and renders per-fact witness marks
with their tier (verified/observed), a red **drift** card, and a purple
**CONFLICT** card. Screenshots in `docs/screenshots/`.

## Provenance

Design history + three first-principles reviews (Codex fact-model, Codex
seamlessness, SigV4/keylog research) live in
`~/git/me/research/2026-05-28-agent-context-observability-design-journey.md` and
the v2 docket item under `~/git/me/docket`.
