# Agent Observatory v0.2.0

Agent Observatory is a local-first macOS app for inspecting what coding agents
actually received: instructions, activated skills, tool catalogs, transcript
evidence, and verified outbound LLM request facts.

This release focuses on making the ambient live-capture path real enough to
trust: non-disruptive release packaging, NetworkExtension-based provider routing,
Codex WebSocket fallback support, and a stricter readiness gate for notarized
public artifacts.

## Highlights

- Native SwiftUI macOS app with demo mode and realtime live feed.
- Go backend/CLI with transcript discovery, context resolver, fact-level
  evidence model, localhost API, SSE stream, and TLS-terminating capture proxy.
- NetworkExtension transparent proxy routes only allowlisted LLM-provider flows
  to Observatory; unrelated traffic is direct-relayed.
- Verified evidence from outbound requests, with raw prompt bodies kept
  in-memory by default and only derived facts persisted.
- Codex `/v1/responses` WebSocket capture support via source-verified 426 →
  HTTP fallback.
- Routing-loop fix: Observatory's own upstream provider dials bypass the
  extension, preventing self-interception and 502s.
- Headless release build path: `make release` no longer runs Finder AppleScript
  or steals GUI focus.
- Strict public release gate: app and DMG must be notarized, stapled, and
  Gatekeeper-accepted before `make release-qa` passes.

## Compatibility

- macOS 26 preview and Xcode 26 for the native app.
- Claude Code: observed transcript support; verified capture through the
  NetworkExtension/proxy path after the agent is restarted to inherit trust.
  Current launch-machine note: live capture is proven, but the final
  capture-and-complete proof is pending because local Claude auth returns 401.
- Codex: observed transcript support and verified capture via HTTP fallback from
  its WebSocket responses transport.
- Antigravity: discovery-only when conversation bodies are opaque `.pb` files.

## Current Limitations

- Already-running agents must restart after capture setup to inherit additive
  trust env vars.
- HTTP/3/QUIC is not captured.
- ECH/no-SNI provider flows fail open and are not captured.
- Inspected provider requests are replayed upstream over HTTP/1.1.
- Session-to-capture correlation is coarse in this release.
- Verified capture requires user-approved NetworkExtension activation and
  login-keychain CA trust.

## Verification Checklist

Before publishing this release, the release owner should have current evidence
for:

- `make release`
- `NOTARY_PROFILE=<profile> make notarize`
- `make release-qa`
- clean `systemextensionsctl` Observatory state
- a real Claude Code run that both captures and completes
- `make v02-readiness`

See `docs/v0.2-readiness.md` and `docs/ne-reset-runbook.md` for the full gates.
