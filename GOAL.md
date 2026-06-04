# Agent Observatory 0.3 Goal

Ship Agent Observatory 0.3 as the safe-by-default Hacker News release: a user can install the macOS app, approve the NetworkExtension and local CA flow, run common coding agents normally, and see useful LLM request evidence without Observatory breaking unknown or custom tools.

## Success Criteria

1. **No broken agents by default.** Unknown provider-bound clients are never locally TLS-terminated unless Observatory can identify them as full-capture eligible. They pass through safely and are reported as uncaptured or metadata-only.
2. **Primary agents captured.** Full request-body capture is verified for the explicit 0.3 support matrix: Claude Code, Codex CLI, and at least one Node-based agent path after trust preflight.
3. **Coverage is visible.** The app and `agents doctor wire` report captured, bypassed, stale-trust, unsupported, and metadata-only states with concrete evidence.
4. **Trust is additive and reversible.** Install never sets global proxy vars or root-replacing CA vars. Uninstall removes daemon state, runtime env, and login-keychain trust.
5. **Failure is fail-open.** Daemon down, unreadable SNI/ECH, unsupported WebSocket, stale runtime trust, and client TLS rejection all preserve the user's request path.
6. **Tests prove behavior.** Automated tests cover provider capture, non-provider pass-through, unknown-client provider pass-through, stale-trust bypass, client TLS failure pause, WebSocket fallback/pass-through, install/uninstall, and diagnostics.
7. **Launch copy is honest.** README/onboarding promise "safe ambient capture for supported coding agents" rather than impossible universal HTTPS body interception.

## Definition Of Done

- `docs/v0.3-safe-capture-spec.md` defines the support matrix, policy, test plan, and release gates.
- The NetworkExtension forwards source metadata to the daemon for policy decisions.
- The daemon performs source-aware capture gating before local TLS termination.
- `agents doctor wire` reflects the same support matrix and trust probes used by capture.
- Full Go, Swift, install QA, and release QA pass.
- A final launch-readiness note says go/no-go for posting.
