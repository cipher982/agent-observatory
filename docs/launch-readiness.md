# Agent Observatory — Launch Readiness

> Go/no-go for a public (HackerNews) launch of v0.1.0.
> Method: full re-audit of the tree against `LAUNCH-AUDIT.md`, two independent
> hatch-codex (gpt-5.5) first-principles reviews, fixes + regression tests,
> then live validation on this Mac. Evidence for criteria 3–6 is captured below.

## Verdict

**<PENDING live validation — see Status>**

The code is launch-grade: every P0/P1 from the original audit is fixed or
consciously scoped, two codex review rounds are addressed, and the full backend
suite (build + vet + race + install-lifecycle) and the macOS app tests are green.
The remaining gate is the live, Apple-gated path: notarize → install → prove
real capture → prove unrelated traffic untouched → prove clean uninstall.

## Status of the falsifiable criteria

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| A1 | Notarized (stapled, `spctl -a -vvv` passes) | ✅ | notarytool Accepted; app + DMG stapled; `spctl -a -vvv` → "accepted / source=Notarized Developer ID" |
| A2 | In /Applications, sysext `activated enabled` | 🟡 one approval away | App in /Applications; extension reaches `[activated waiting for user]` in `systemextensionsctl list` (every code-level gate passed). Needs the one-time System Settings approval toggle. |
| B3 | Live capture of a real agent with correct host + tool names (screenshot) | ⏳ blocked on A2 approval | — |
| B4 | Unrelated traffic untouched (`example.com` + plain HTTP) | ⏳ blocked on A2 approval | — |
| B5 | Fragmentation stress: SNI fix holds, no provider crashes | ⏳ blocked on A2 approval | SNI parser fragmentation tests pass headlessly (`SNITests.testNoCrashOnAnyPrefix`) |
| C6 | Uninstall fully reverses (sysext gone, CA trust removed, traffic fine) | ⏳ blocked on A2 approval | `agents uninstall` + `trust remove` covered by lifecycle QA |
| D7 | Launch-blocker sweep: every P0/P1 fixed or deferred w/ rationale | ✅ | sweep table below |
| D8 | README/onboarding match shipped NE reality | ✅ | NE-first copy across README, onboarding, doctor, launch-note |
| D9 | Final independent review returns no P0/P1 | ✅ | two codex rounds; no P0, residual items are P2 (below) |

## Launch-blocker sweep (D7) — original audit items vs current tree

| Audit ID | Issue | Resolution |
|----------|-------|------------|
| P0-1 | App spawns its own engine on the daemon's ports w/ ephemeral CA | **Fixed.** App is a true renderer: never spawns a live engine (would mint an untrusted CA); demo runs on distinct ports (7880/7881); mode switches serialize via a restart chain so ports are released before respawn. (`EngineClient.swift`) |
| P0-2 | Proxy buffers the full upstream response → breaks streaming | **Fixed.** `forward()` returns the live upstream conn; `pump()` streams `resp.Write(client)` then closes. Regression test `TestStreamingResponseIsNotBuffered`. (`wire/proxy.go`) |
| P0-3 | Global env install breaks unrelated traffic (HTTP_PROXY 405; SSL_CERT_FILE/AWS_CA_BUNDLE replace roots) | **Fixed by design change.** NE owns routing, so the install sets NO proxy vars and NO root-replacing vars — only the additive `NODE_EXTRA_CA_CERTS`. Blast radius eliminated. (`install/install.go`, asserted in `install-qa.sh`) |
| P1-1 | Wildcard CORS exposes local data to any website | **Fixed (pre-goal).** `withCORS` echoes only loopback origins. (`serve.go`) |
| P1-2 | Send-on-closed-channel panic race in the daemon | **Fixed (pre-goal).** `Subscribe` no longer closes the channel; `TestSubscribeUnsubscribeRace` under `-race`. (`wire/server.go`) |
| P1-3 | No LICENSE | **Fixed.** Apache-2.0 at repo root. |
| P1-4 | Product-name inconsistency ("Agent Context Observatory") | **Fixed.** All occurrences renamed to "Agent Observatory" (main.go, qa.sh). |
| P2 | Unbounded capture growth | **Fixed.** 500-entry ring buffer. (`wire/server.go`) |
| P2 | "Engine unavailable" full-screen on transient startup | **Mitigated.** Live mode shows a specific "daemon not running" message; poll loop retries before surfacing failure. |
| P2 | Uninstall not literally complete | **Fixed.** `RemoveAll(StateDir)` + keychain trust removal. |
| P2 | Install only UPPERCASE proxy vars | **Moot.** Install no longer sets proxy vars (NE routes). |
| P2 | Stable CA never validated on load | **Fixed.** `usableAt()` regenerates an expired/invalid CA on load (not a still-valid one). Tests `TestStableCAReused…`, `TestExpiredStableCAIsRegenerated`. |
| P2 | Ambient daemon captures don't feed the verified tier | **Fixed.** The daemon registers `WireObservations` from its in-memory captures, filtered by host→runtime. Test `TestObservationsForRuntimeFiltersByHost`. |
| P2 | `agents serve` undocumented | Present in README Commands table. |
| P2 | launch-note overclaims | **Fixed.** NE framing added. |
| P3 | Onboarding fabricated `max(count,N)` metrics | **Fixed.** Shows real counts. |
| P3 | Menu-bar `LSUIElement` decision | Deferred (P3 cosmetic; Dock icon intentional for v0.1). |
| P3 | `EngineClient.stop()` doesn't waitUntilExit | **Fixed.** `stopAndWait()` + serialized restarts. |
| P3 | App-spawned helper pipes never drained | **Fixed.** readabilityHandlers drain stdout/stderr. |
| P3 | CA PEM perms inconsistent | Cosmetic; cert is public. Deferred. |
| P3 | `truncate` byte-slices multibyte | Display-only. Deferred. |

## Residual items (consciously deferred, none P0/P1)

- **Coarse session↔capture correlation.** Ambient captures attribute to a
  runtime's session by host, not per-request. Honest scope for v0.1; the live
  feed and verified tier both work, per-request correlation is a refinement.
- **HTTP/2 downgrade.** The MITM path speaks HTTP/1.1 to inspected hosts.
  Providers accept it; documenting rather than implementing h2 for v0.1.
- **Go/Swift allowlist duplication.** Two implementations, kept in sync by
  mirrored contract tests (`proxy_test.go` ↔ `SNITests.swift`).
- **ECH.** If a provider enables Encrypted ClientHello, SNI becomes unreadable
  and that provider's capture silently stops (fail-open). Revisit if it happens.

## Reproducing the QA

```bash
cd backend && bash scripts/qa.sh      # build + vet + short tests + race + install lifecycle
make app-build                        # macOS app (Developer ID signed)
# app unit tests:
xcodebuild -project app/Observatory.xcodeproj -scheme ObservatoryTests \
  -derivedDataPath /tmp/observatory-dd test
```

## Live validation evidence (criteria 3–6)

_Filled in during the interactive notarize/install/capture/uninstall run._
