# Agent Context Observatory Launch Note

I built a local macOS app that shows what context coding agents actually
received: instructions, activated skills, available tools, transcript evidence,
and live outbound LLM requests.

The problem is simple: agent context is invisible until it fails. If a coding
agent misses a repo rule or silently lacks a tool, there is usually no debugger
for the assembled prompt/tool state. This app makes that state inspectable.

The first release is local-first. It reads local transcripts passively, verifies
outbound requests through an install-once local proxy, and stores only derived
facts instead of raw prompt bodies. After install, people use their agents
normally; there is no wrapper command or hosted account in the primary flow. The
native app includes a demo mode so the product is visible immediately, then a
live mode for real local traffic.

What is included:

- SwiftUI macOS app with realtime live feed.
- Native Dock icon and menu bar extra.
- Go backend/CLI with transcript discovery, context resolver, fact/evidence
  model, localhost API, SSE stream, and optional HTTPS proxy.
- Install/status/uninstall flow for ambient capture.
- Release build and QA commands.

What is not included yet:

- Hosted/team observability.
- App Store distribution.
- Canonical context management across every runtime.

Repo: https://github.com/cipher982/agent-observatory

The project is meant to be the missing inspector/debugger for agent context. I
would especially like feedback on the install-once capture model, the local MITM
security framing, and which agent runtimes should be hardened first.
