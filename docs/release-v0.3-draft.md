# Agent Observatory v0.3.0

Agent Observatory 0.3 is the safe-by-default NetworkExtension release. It keeps
the install-once macOS experience, but narrows full HTTPS body capture to
supported, trust-ready coding agents. Unknown and stale clients are passed
through and reported as coverage gaps instead of being broken by a local TLS
intercept.

## Highlights

- Source-aware NetworkExtension capture policy: provider host is not enough;
  full capture also requires a supported source runtime with current Observatory
  trust.
- Source PID/env trust evidence is centralized for the daemon policy and
  `agents doctor wire`; missing or stale evidence is a pass-through condition,
  not a broken MITM attempt.
- Claude Code and Codex CLI are the primary supported full-capture targets for
  0.3.
- Gemini CLI is visible as a candidate runtime pending live release proof.
- Hatch/OpenCode stale persistent servers are reported as diagnostics, not
  treated as launch blockers or patched manually.
- Unsupported/custom provider-bound clients are tunneled opaquely by default and
  surfaced through `/api/coverage` and the live feed.
- Fail-open pause circuit breaker: if a client rejects Observatory's CA, future
  capture is paused instead of repeatedly breaking requests.
- Release QA includes `make v03-safe-capture-qa`, which proves one captured
  supported-source provider request and one tunneled unknown-source provider
  request without requiring real API credentials.
- Installed-daemon compatibility QA includes `make v03-installed-daemon-compat-qa`,
  which proves provider-bound traffic without v0.3 source metadata is tunneled
  and recorded as pass-through coverage without increasing body captures.

## Compatibility

- macOS app and NetworkExtension transparent proxy.
- Claude Code: supported when the source process is Claude Code and
  `NODE_EXTRA_CA_CERTS` points at the current Observatory CA.
- Codex CLI: supported when the source process is Codex and
  `CODEX_CA_CERTIFICATE` points at the current Observatory CA.
- Gemini CLI: candidate when attributable to Gemini's Node process and current
  `NODE_EXTRA_CA_CERTS`.
- Unknown tools, stale long-lived runtimes, Python/Requests tools, VS Code
  extensions, and Electron/custom wrappers: metadata/pass-through by default.

## Verification Checklist

Before publishing this release, the release owner should have current evidence
for:

- `make release`
- `make v03-safe-capture-qa`
- `make v03-installed-daemon-compat-qa`
- `make notarize`
- `make release-qa`
- installed `/Applications/Agent Observatory.app` reports `0.3.0/7`
- system extension `0.3.0/7` is `[activated enabled]`
- live Claude Code and Codex requests complete and appear as full captures
- an unsupported provider-bound client completes, produces no body capture, and
  appears as a pass-through coverage event

See `docs/v0.3-safe-capture-spec.md`, `docs/v0.3-launch-readiness.md`, and
`docs/ne-reset-runbook.md` for the full gates.
