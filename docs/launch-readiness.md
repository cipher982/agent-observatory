# Agent Observatory — Launch Readiness

> Go/no-go for a public (HackerNews) launch of v0.2.0.
> Method: full re-audit of the tree against `LAUNCH-AUDIT.md`, two independent
> hatch-codex (gpt-5.5) first-principles reviews, fixes + regression tests,
> then live validation on this Mac. Evidence for criteria 3–6 is captured below.

## Verdict

**NO-GO until the host system-extension state is clean, a real Claude Code run both captures and completes, and the v0.2.0 release is published or staged. Do not post yet.**

> Correction (2026-06-01): an earlier draft of this doc said GO. That was WRONG.
> It declared B3 "proven" because a capture event appeared in the live feed —
> but the agent's request never actually reached the provider. Live testing with
> a REAL agent (Codex) exposed two defects a curl-with-CA test had masked. The
> capture/parse/UI work is genuinely solid; the *forwarding* path was broken.

### The two defects found by real-agent testing

1. **Routing loop (P0, affected ALL capture). — FIXED & VERIFIED LIVE.** The Go
   proxy terminates TLS, then dials the real provider:443 to forward — but that
   dial was itself a provider :443 flow, so the NE extension **re-intercepted the
   proxy's own upstream** and looped it back. Forward never reached the provider;
   the agent got 502. A capture event still appeared, which is exactly why the
   earlier "GO" was wrong. **Fixed** (`handleNewFlow` bypasses flows whose
   `sourceAppSigningIdentifier` is our daemon; helper signed with a stable id) and
   **verified live on 2026-06-01** using dev-scoped capture (only a signed test
   harness intercepted; host agents untouched): the harness got real provider
   responses through the proxy — `api.openai.com/v1/models` → **HTTP 401**,
   `api.anthropic.com/v1/messages` → **HTTP 405** — NOT 502. The forward reaches
   the real provider; no loop.

2. **Codex WebSocket transport — SOLVED.** Codex's primary transport is
   `wss://api.openai.com/v1/responses`, which we can't usefully parse and which
   strict WS clients reject when proxied. Source dive (codex rust-v0.134.0
   `core/src/client.rs:1402-1405`) found that a **426 Upgrade Required** on the WS
   connect maps straight to `FallbackToHttp` with no retry delay — and the
   resulting `/v1/responses` HTTP request is one we fully TLS-terminate and parse.
   So the proxy now replies **426** to provider WS upgrades. Verified against a
   real Codex (isolated via HTTPS_PROXY): one 426 → completed (~7s) →
   **full capture: system 43000 chars, 22 tools**. The earlier
   "Codex ignores our CA / unsupported" conclusion was a symptom of the broken
   extension state (routing loop + the CA not reaching Codex), NOT a real verdict.
   **Codex is supported.**

### What IS solid (unchanged by the above)

- Transcript discovery, context resolver, fact/evidence model, the live-feed UI,
  notarized Developer-ID signed app + system extension that activates, the
  TLS-terminating parse (correct host/endpoint/tools/prompt-length on real
  bodies), unrelated-traffic pass-through, fail-open when the daemon is down, and
  a now-reliable kill switch (menu-bar Disable + keychain CA sweep on uninstall).
  Full backend suite + app tests green. Five codex review rounds addressed.

### What must happen before a real GO

- **DONE:** the routing-loop fix is verified live (dev-scoped harness → real
  provider 401/405, no loop). No VM needed — `docs/scoped-capture-dev.md` shows
  the safe on-host method.
- **Remaining:** clear the host's stale system-extension state, repair local
  Claude Code authentication, then run a real
  **Claude Code** (HTTP-path) turn under live NE capture and confirm it both
  captures AND completes. Current v0.2 global capture emits real Claude Code
  `api.anthropic.com` events, but the local CLI exits 401 before completion.
- **DONE:** the no-reboot Disable Capture fix is rebuilt, notarized, stapled,
  and installed from current `HEAD`. Disable now stops only the
  transparent-proxy tunnel and removes CA trust, so users can turn capture back
  on without deactivating the system extension. This Mac still needs a reboot
  because an older build already left macOS in `terminated waiting to uninstall
  on reboot`.
- Codex is SUPPORTED (426 → HTTP fallback, fully captured); no longer scoped out.

The caveats a poster must own regardless (in README Known Limitations): per-runtime
CA hints; already-running agents must restart (app warns); HTTP/3/QUIC not
captured; ECH fails open; inspected hosts proxied over HTTP/1.1.

## Isolated re-verification runbook (do this before GO)

Host disk was 99% full (~12 GB) — free ~60 GB first, then:

```bash
# 1. macOS guest VM (NAT networking fully isolates the host network stack)
tart clone ghcr.io/cirruslabs/macos-sequoia-xcode:latest ao-test   # or a macOS 26 image
tart run --dir=repo:~/git/agent-observatory ao-test &

# 2. inside the VM: disable SIP (recovery) to skip notarize-every-build, enable dev mode
#    (tart run --recovery → csrutil disable → reboot)
sudo systemextensionsctl developer on

# 3. build + install + activate the app in the VM, approve the sysext once (harmless in VM)

# 4. PROVE the loop fix (the test the earlier GO skipped):
curl --cacert ~/.local/state/agent-observatory/ca/observatory-ca.pem \
  https://api.openai.com/v1/models        # expect a real 401, NOT 502
#    daemon log must NOT show: upstream tls handshake: x509: unknown authority

# 5. PROVE a real agent: launch Claude Code in the VM, run a prompt,
#    confirm the capture appears AND the agent completes normally.
```

## Status of the falsifiable criteria

| # | Criterion | Status | Evidence / note |
|---|-----------|--------|----------------|
| A1 | Notarized (stapled, `spctl -a -vvv` passes) | ✅ | Current `b45d367` artifacts are Developer ID signed, notarized, stapled, and Gatekeeper-accepted. `make release-qa` passes; `make v02-readiness` passes the artifact and notarization sections. |
| A2 | In /Applications, sysext `activated enabled` | ⚠️ host needs reboot after old disable path | The v0.2 app/daemon/CA were previously clean and enabled. Testing the old Disable action intentionally deactivated the sysext and macOS now reports `0.2.0/5` `terminated waiting to uninstall on reboot`. Reboot, install the fixed build, then approve/start capture again. |
| B3 | Live capture of a real agent, captured AND agent completes | ⚠️ partial | Current v0.2 live NE capture emits real Claude Code requests (`api.anthropic.com`, `anthropic/messages`, ~27k system chars, 11 tools) and controlled Anthropic probes forward to real upstream 401s. Still TODO: local Claude auth must be repaired so a real Claude Code turn completes normally while captured. |
| B4 | Unrelated traffic untouched | ✅ | While active, `example.com:443` kept its real **Cloudflare** cert (not Observatory CA), plain HTTP 200, **0 captures** during unrelated traffic; only `api.openai.com` presented the Observatory CA. |
| B5 | Fail-open + stability | ✅ | Stopping the daemon reverted providers to real certs (NE fail-open → agents keep working — verified repeatedly). Client CA-reject → `clientTLSFailures` on `/healthz` → app warns. SNI fragmentation tests pass. |
| C6 | Disable/uninstall reverses | ⚠️ host proof pending | `agents uninstall` cleans daemon/state/env/keychain trust. The notarized app Disable action now stops the tunnel and removes CA trust without deactivating the sysext, avoiding the prior reboot-required off/on loop. Needs one clean post-reboot enable/disable/re-enable proof from the fixed build. |
| — | Routing loop fixed | ✅ verified live | `handleNewFlow` bypasses the daemon's own upstream flows; proven on-host via dev-scoped harness (real 401/405, no loop). |
| — | Codex (WebSocket) capture | ✅ via 426→HTTP fallback | Proxy replies 426 to provider WS upgrades; Codex falls back to HTTP instantly and is fully captured (43k sys chars, 22 tools). Verified via explicit-proxy isolation; shared NE routing path is now active for provider flows. |
| — | Safe on-host iteration | ✅ | dev-scope allowlist (`/tmp/agent-observatory-dev-scope`) + signed `devharness` + menu kill switch; lets us test the real kernel path without a VM or risking host agents. See `docs/scoped-capture-dev.md`. |
| D7 | Launch-blocker sweep: every original-audit P0/P1 fixed | ✅ | sweep table below (the routing loop was a NEW P0 found later by live testing) |
| D8 | README/onboarding match shipped NE reality | ✅ | NE-first copy across README, onboarding, doctor, launch-note; Codex supported via 426→HTTP |
| D9 | Final independent review returns no P0/P1 | ⚠️ | static-review rounds were clean, but LIVE testing then found the routing-loop P0 — the lesson being that static review can't catch a kernel-routing loop. Re-run after live VM verification. |

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

Captured on this Mac (macOS 26, 2026-06-02) with the notarized v0.2.0 build.

**A2 — extension activated:**
```
$ systemextensionsctl list | grep agentobservatory
*	*	M49WM6JSW8	com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension (0.2.0/5)	Agent Observatory Proxy	[activated enabled]
```

**B3 — live capture (SSE feed):**
```
{"type":"capture","host":"api.openai.com","endpoint":"openai/chat.completions","runtime":"codex","systemChars":41,"parsed":true,"toolCount":1,"toolNames":["mcp__launch__verify"]}
{"type":"capture","host":"api.anthropic.com","endpoint":"anthropic/messages","runtime":"claude","systemChars":42,"parsed":true,"toolCount":2,"toolNames":["mcp__launch__verify","Bash"]}
{"type":"capture","host":"api.anthropic.com","endpoint":"anthropic/messages","runtime":"claude","systemChars":27281,"parsed":true,"toolCount":11,"toolNames":["Agent","AskUserQuestion","Bash","Edit","Glob","Grep","Read","ScheduleWakeup","Skill","ToolSearch","Write"],"at":"2026-06-02T13:35:12-04:00"}
```

The 2026-06-02 capture above came from real Claude Code CLI requests through the
approved v0.2 NetworkExtension. The CLI returned `401 Invalid authentication
credentials`, so this proves capture/routing but not completion.

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
