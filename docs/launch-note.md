# Agent Observatory Launch Note

I built a local macOS app that shows what context coding agents actually
received: instructions, activated skills, available tools, transcript evidence,
and live outbound LLM requests.

The problem is simple: agent context is invisible until it fails. If a coding
agent misses a repo rule or silently lacks a tool, there is usually no debugger
for the assembled prompt/tool state. This app makes that state inspectable.

The first release is local-first. It reads local transcripts passively, verifies
outbound requests through a local proxy, and stores only derived facts instead of
raw prompt bodies. Capture ingress is a macOS NetworkExtension transparent proxy:
a signed, user-approved system extension routes only allowlisted LLM-provider
flows to the local proxy, so unrelated traffic is untouched by construction —
there is no global HTTPS_PROXY hijack. After setup, people use their agents
normally; newly launched agents are captured without a wrapper command or hosted
account (already-running shells are captured after they restart). The native app
ships as a DMG with the normal app-to-Applications drag install, includes a demo
mode so the product is visible immediately, then exposes live capture as a
separate opt-in setup inside onboarding.

What is included:

- SwiftUI macOS app with realtime live feed.
- Native Dock icon and menu bar extra.
- Go backend/CLI with transcript discovery, context resolver, fact/evidence
  model, localhost API, SSE stream, and a TLS-terminating capture proxy.
- NetworkExtension transparent proxy for routing only provider flows.
- Install/status/uninstall flow for ambient capture, with login-keychain CA trust.
- Developer ID signed/notarized DMG release flow, plus QA commands.

What is not included yet:

- Hosted/team observability.
- App Store distribution.
- Canonical context management across every runtime.

Repo: https://github.com/cipher982/agent-observatory

The project is meant to be the missing inspector/debugger for agent context. I
would especially like feedback on the install-once capture model, the local MITM
security framing, and which agent runtimes should be hardened first.
