# Agent Observatory — Launch Readiness

> Go/no-go for a public (HackerNews) launch of v0.1.0.
> Method: full re-audit of the tree against `LAUNCH-AUDIT.md`, two independent
> hatch-codex (gpt-5.5) first-principles reviews, fixes + regression tests,
> then live validation on this Mac. Evidence for criteria 3–6 is captured below.

## Verdict

**GO — with the honest caveats below called out, not buried.**

Every falsifiable criterion (A1–C6) is proven live on this Mac, and the
launch-blocker sweep (D7–D9) is clean. The product does what it claims: it
captures real agent→provider traffic with correct facts, leaves unrelated
traffic untouched, fails open safely, and uninstalls cleanly. Four codex review
rounds (two architecture, two hands-on debugging + a web-researched 0→1) are
addressed; the full backend suite (build + vet + race + install-lifecycle) and
macOS app tests are green.

The caveats a poster must own (all documented in the README's Known Limitations):
- Capture requires a per-runtime CA hint for the runtimes that ignore the macOS
  keychain — Node/Claude Code (`NODE_EXTRA_CA_CERTS`) and Codex
  (`CODEX_CA_CERTIFICATE`); only the AWS Go SDK (Bedrock) needs nothing. An
  **already-running** agent fails provider TLS until restarted; the app now warns
  on this instead of failing silently.
- HTTP/3/QUIC isn't captured; ECH fails open; inspected hosts are proxied over
  HTTP/1.1.
- `agents uninstall` (CLI) can't deactivate the system extension; it says so and
  points to the app / System Settings.

## Status of the falsifiable criteria

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| A1 | Notarized (stapled, `spctl -a -vvv` passes) | ✅ | notarytool Accepted; app + DMG stapled; `spctl -a -vvv` → "accepted / source=Notarized Developer ID" |
| A2 | In /Applications, sysext `activated enabled` | ✅ | After approval, `systemextensionsctl list` shows `* * M49WM6JSW8 …TransparentProxyExtension (0.1.0/1) [activated enabled]`; provider process running |
| B3 | Live capture of a real agent with correct host + tool names | ✅ | SSE feed captured `api.openai.com` → `openai/chat.completions` (tool `mcp__launch__verify`) and `api.anthropic.com` → `anthropic/messages` (tools `mcp__launch__verify`,`Bash`) with correct runtime/host/sys-chars. Earlier run screenshot: `docs/screenshots/live-feed.png` |
| B4 | Unrelated traffic untouched (`example.com` + plain HTTP) | ✅ | While capture active: `example.com:443` served its real **Cloudflare** cert (not Observatory CA), `github.com` HTTP 200, plain `http://example.com` HTTP 200; **0 captures** during unrelated traffic. Contrast: `api.openai.com` presented the **Observatory** CA. |
| B5 | Fail-open + stability under handshake failure | ✅ | Stopping the daemon reverted `api.openai.com` to its real **Google** cert (NE fail-open, agents keep working). A client rejecting the CA produced `clientTLSFailures` on `/healthz` → app warns instead of breaking silently. SNI fragmentation: `SNITests.testNoCrashOnAnyPrefix`. |
| C6 | Uninstall fully reverses | ✅ | `agents uninstall`: daemon gone, **0** trusted CAs left in keychain (hash sweep), state dir gone, env block removed, plist gone. Post-uninstall every host (incl. providers) back on real certs, HTTP 200. CLI honestly notes the system extension must be removed via the app/System Settings. |
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

Captured on this Mac (macOS 26, 2026-06-01) with the notarized v0.1.0 build.

**A2 — extension activated:**
```
$ systemextensionsctl list | grep agentobservatory
*	*	M49WM6JSW8	com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension (0.1.0/1)	Agent Observatory Proxy	[activated enabled]
```

**B3 — live capture (SSE feed):**
```
{"type":"capture","host":"api.openai.com","endpoint":"openai/chat.completions","runtime":"codex","systemChars":41,"parsed":true,"toolCount":1,"toolNames":["mcp__launch__verify"]}
{"type":"capture","host":"api.anthropic.com","endpoint":"anthropic/messages","runtime":"claude","systemChars":42,"parsed":true,"toolCount":2,"toolNames":["mcp__launch__verify","Bash"]}
```

**B4 — unrelated untouched (cert issuer contrast, capture active):**
```
example.com      → Cloudflare TLS Issuing ECC CA 3   (real cert, not MITM'd)
api.openai.com   → Agent Observatory Local CA        (allowlisted → intercepted)
http://example.com → HTTP 200 ; 0 captures during unrelated traffic
```

**B5 — fail-open + health-check:**
```
# daemon stopped → NE fails open:
api.openai.com   → Google Trust Services WE1         (back to real cert)
# client that doesn't trust the CA → daemon reports it:
GET /healthz → {"clientTLSFailures":3,"lastTLSFailHost":"api.openai.com"}
# → app shows a yellow "an agent rejected the capture certificate" banner
```

**C6 — clean reversal (`agents uninstall`):**
```
daemon: gone ✓   keychain Observatory CAs: 0 ✓   state dir: gone ✓
env block: 0 ✓   plist: gone ✓
post-uninstall: example.com/api.openai.com/api.anthropic.com/github.com all on
their real certs; example.com & api.github.com → HTTP 200.
(CLI prints: the system extension is still active — remove via the app or
System Settings → Login Items & Extensions.)
```

## Stretch / follow-ups (not launch blockers)

- A 20s screen capture of the live feed for the HN post (have a still:
  `docs/screenshots/live-feed.png`).
- The bundled `agents` helper auto-setting `CODEX_CA_CERTIFICATE` covers fresh
  Codex launches; an already-running Codex still needs a restart (now warned).
- Per-request session↔capture correlation (currently host→runtime coarse).
