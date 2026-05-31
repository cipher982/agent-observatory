# Agent Observatory — Risky Architecture Decisions (P0s)

> Companion to `LAUNCH-AUDIT.md` and `docs/launch-plan.md`. This doc is for the
> launch blockers that need a hardening decision because the wrong choice means
> "Observatory broke my machine / my agent." Findings are sourced from the parallel
> web research (`docs/architecture-research.md`).
>
> **Product thesis (locked):** the ambient, install-once TLS MITM is the product and
> the pitch — "install once, use your agents normally, we just see the traffic." We are
> NOT pivoting to base_url (that's the commodity tracing-app pattern and the friction we
> exist to remove). The P0s below are about making the MITM *correct and fail-open*, not
> about replacing it.

## The root problem (one sentence)

The MITM is intentionally in the path of agent calls — that's the whole point. The defect is that it's currently a **hard dependency that fails *closed*** (global env pinned to one always-on daemon, a CA-only trust file that breaks unrelated HTTPS, `HTTP_PROXY` that 405s plain HTTP). The fix is to make the same in-path MITM **fail *open*** — capture is best-effort, traffic delivery is guaranteed — and to shrink its collateral footprint so it only ever affects the LLM-provider hosts it inspects.

Everything below follows from that. The P0-3 issues are three faces of "in-path but fail-closed"; the fix is "in-path but fail-open + minimal footprint," not "get out of the path."

---

## P0-1 — App vs. daemon collision (ports + CA)

**What:** the app spawns its own `monitor` on the daemon's fixed ports with an ephemeral CA; agents trust the daemon's *stable* CA. Result: port fight + TLS failures in Live mode.

**This one is not actually a hard design question — it's a clear bug with a clear fix.** The code's own comment ("the app is a pure renderer") states the right design. Decision is just *confirm the contract*:

- **Recommended:** App is a **pure renderer**. On launch, `GET /healthz`; if a daemon answers, render from it and never spawn. Only self-spawn an engine when none exists (the not-installed demo case), and give that demo engine **its own ephemeral ports** (`:0` auto-assign) + in-app-only synthetic data so it can never collide with the installed daemon. Never spawn a *live* engine from the app at all — live capture is exclusively the installed daemon's job.
- Guard: app shows a clear "connected to daemon vX on :7878" vs "demo (no daemon)" state, so the two modes are never ambiguous.

→ This is a Wave-1 implementation task, not a research question. Low conceptual risk once we commit to "renderer-only."

---

## P0-2 — Streaming responses

**What:** proxy `io.ReadAll`s the whole upstream response, breaking token streaming for inspected hosts.

**Also not a deep design question — it's a fix with one correctness caveat.** We already capture everything we need from the *request* body. The response only needs to be relayed.

- **Recommended:** after capturing the request, write status + headers to the client, then `io.Copy` the body straight through (preserving chunked/`text/event-stream` framing). Do **not** buffer, do **not** set a synthetic `Content-Length`.
- Caveat to verify: if we ever want response-side facts (token usage, stop reason), tee the stream (`io.TeeReader`) into a lightweight parser that **runs after the bytes are already forwarded**, never gating client delivery on our parse. Forward-first, observe-second.

→ Wave-1 implementation task. The only "decision" is forward-first vs parse-first, and forward-first is unambiguously correct for an observability tool.

---

## P0-3 — Env-var blast radius (the genuinely hard one)

This is where the real architectural decision lives. Three sub-problems, each a fail-open question:

### (a) `HTTP_PROXY` → 405 on plaintext HTTP
The proxy is CONNECT-only. Setting `HTTP_PROXY` routes plain `http://` through it and gets a 405.
- **Cheap fix:** don't set `HTTP_PROXY` at all (LLM providers are all HTTPS); set only `HTTPS_PROXY`. Add `NO_PROXY`/`no_proxy` for `localhost,127.0.0.1,::1,*.local`.
- **Fuller fix:** implement absolute-form HTTP forwarding (RFC 7230) so the proxy can pass plaintext too. More surface area; probably not worth it for v0.1.

### (b) `SSL_CERT_FILE` / `AWS_CA_BUNDLE` *replace* the trust store — **CONFIRMED by research**
Pointing these at a file containing **only** our CA makes every other HTTPS fail for processes that honor them. This is the scariest one. Research (authoritative docs) confirms the exact behavior:

| Env var | Stack | Behavior |
|---|---|---|
| `NODE_EXTRA_CA_CERTS` | Node | **Additive** — "well-known root CAs … will be *extended*" ([nodejs.org/api/cli](https://nodejs.org/api/cli.html)) |
| `SSL_CERT_FILE` | OpenSSL | **Replaces** the default CA file ([docs.openssl.org](https://docs.openssl.org/3.3/man3/SSL_CTX_load_verify_locations/)) |
| `SSL_CERT_DIR` | OpenSSL | **Replaces** the default dir — *but* accepts a delimiter-separated list, so `<system_dir>:<our_dir>` is union-like ([X509_get_default_cert_file](https://docs.openssl.org/master/man3/X509_get_default_cert_file/)) |
| `AWS_CA_BUNDLE` | AWS CLI/boto3 | **Replaces** ([docs.aws.amazon.com](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html)) |
| `REQUESTS_CA_BUNDLE` / `CURL_CA_BUNDLE` | requests / curl | **Replaces** |

- **The fix is to make trust ADDITIVE, not replacement:**
  - `NODE_EXTRA_CA_CERTS` is already additive — keep it as-is.
  - For OpenSSL/AWS/requests: instead of a CA-only file, write a **bundle = (macOS system roots) + our CA** and point `SSL_CERT_FILE`/`AWS_CA_BUNDLE`/`REQUESTS_CA_BUNDLE`/`CURL_CA_BUNDLE` at *that combined bundle*. Then unrelated HTTPS still validates because the system roots are present. (Source the system bundle from the macOS keychain via `security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain`, or vendor a maintained CA bundle, refreshed on install.)
  - Implementation note: regenerate the combined bundle on install and whenever the CA rotates; the daemon already owns the CA lifecycle so it can own bundle assembly too.
  - Alternative considered: scope these vars to only intercepted runtimes — rejected as more fragile than a correct additive bundle.

### (c) The daemon is a single point of failure (fail-open)
Even with (a)/(b) fixed, `HTTPS_PROXY=127.0.0.1:7879` means **if the daemon is down, new agent connections can't reach a proxy and fail.** This is the deepest issue and the heart of the user's "robust guards" instinct.

This is where the two user ideas come in:

#### Idea A — "Install on reboot" (safest, clunky)
Defer activation so env only takes effect on next login, when the daemon is guaranteed loaded by launchd first.
- **Pro:** avoids the "I set env but daemon isn't up yet" window; clean propagation to GUI apps too.
- **Con:** clunky UX ("restart to activate"); doesn't solve fail-open *after* boot if the daemon later crashes.
- **Verdict (preliminary):** necessary-ish for clean *propagation* (env only reaches newly launched processes anyway), but it is **not** a substitute for fail-open. It's a UX honesty fix, not a safety mechanism.

#### Idea B — "In-flight transition proxy that keeps things working" (creative, first-principles) — **research-validated, refined**
Your instinct is right, but the research refined *where* it belongs: make it a **permanent always-on dumb shim, not a transition-only mechanism.** This is the single highest-leverage guard.
- **Always-on dumb shim on the port (THE key guard):** a tiny, boring CONNECT-tunneling forward proxy permanently owns `:7879`. The "smart" capturing engine sits behind it. If the capture engine crashes/restarts/upgrades, the shim **degrades to plain pass-through tunneling** — traffic keeps flowing, you only lose *capture*, never *connectivity*. This converts the daemon from a hard SPOF into a soft one. Critically, the research notes CLIs honoring `HTTPS_PROXY` have **no DIRECT-fallback concept** (unlike browsers), so the shim — not PAC — is what actually protects agent CLIs.
- **Fail-open within the proxy:** if capture/parse fails for any reason, fall through to blind `io.Copy` instead of erroring. Capture best-effort; delivery guaranteed.
- **PAC `DIRECT` fallback:** useful belt-and-suspenders for *browser* traffic, but research confirms **most agent CLIs ignore PAC** — so it's a bonus, not the mechanism.
- **launchd `KeepAlive`:** fast restart (must fail-fast, not crash-loop, or launchd throttles respawns).
- **Healthcheck-gated env:** only export proxy vars while `127.0.0.1:PORT` answers; ship a clean panic/uninstall (`launchctl unsetenv` + strip `~/.zshenv` block) so a user can never get wedged.
- **Transparent interception (NetworkExtension / pf):** fail-open-ish by construction, but see the NetworkExtension section — defer past v0.1.

**Synthesis (research-confirmed):** the cheapest robust v0.1 is the **always-on dumb shim (fail-open) + additive CA bundle + HTTPS-only env (no `HTTP_PROXY`) + verbose `NO_PROXY` + healthcheck-gated env with a panic/uninstall.** "Install on reboot" is honest UX for env *propagation* but is **not** the safety mechanism — and the research found reboot is mostly conservative: new processes only need `launchctl setenv` (login LaunchAgent) + `~/.zshenv` + **quit/relaunch the agent**; reboot is only strictly needed so already-running daemons re-launch. So ship "relaunch your agent" as default, "reboot" as belt-and-suspenders.

---

## Decision summary (what needs your call)

| P0 | Conceptual risk | Recommendation | Needs your decision? |
|---|---|---|---|
| P0-1 ports/CA | Low (clear bug) | App = pure renderer; demo on auto-port + in-app data | No — just implement |
| P0-2 streaming | Low (clear fix) | Forward-first, observe-second (`io.Copy`, optional `TeeReader`) | No — just implement |
| P0-3a HTTP_PROXY | Low | Drop `HTTP_PROXY`; HTTPS-only + `NO_PROXY` | No — just implement |
| P0-3b CA replace | Medium | **Additive bundle** (system roots + our CA), not CA-only | Confirm approach (research finishing) |
| P0-3c fail-open | **High** | Always-on dumb shim (pass-through when engine down) + launchd KeepAlive + healthcheck-gated env + panic/uninstall | **Yes — confirm the guard set** |

The genuinely open work is **P0-3c**: how much fail-open machinery to ship for v0.1. This is a *hardening* decision, not an architecture pivot.

> **DECISION (David, locked):** The ambient install-once MITM **is the product and the pitch.** "Install once, use your agents normally, we just see the traffic." We are NOT pivoting to base_url. The P0-3 issues are bugs in the MITM implementation, not reasons to abandon it — and they're all fixable without touching the ambient UX. NetworkExtension is the *evolution* of this pitch (ambient capture with no env hijack), not a detour. David has a paid Apple Developer account, so NE is on the table as the v0.2 capture layer.

---

## Why MITM stays the product (rejecting the base_url pivot)

The ambient MITM is the **moat**. The README's literal differentiator: *"No wrapper command in the primary flow. No managed launch. No browser extension. Newly launched agents are captured automatically."* That is the whole pitch.

**`base_url` would destroy it:**
- It's per-agent, per-config **active setup every time** — exactly the friction the product exists to eliminate.
- It's what `claude-meter` / LiteLLM / Helicone / Portkey already do → pivoting commoditizes Observatory into "yet another LLM tracing app."
- It can't capture agents you didn't pre-configure, and breaks the instant a user adds a new agent or changes a config.

**So `base_url` is demoted to:** an *optional* power-user escape hatch for runtimes that pin certs or can't be intercepted — never the default, never in the pitch, never required.

### The reframe that matters

The prior-art research (claude-meter, LiteLLM, mitmproxy) is still useful — but as **evidence of what the safe-but-boring crowd does**, i.e. what we are deliberately NOT. Observatory's bet is that ambient, zero-config, install-once capture is worth the engineering to do MITM *correctly*. The three P0-3 problems are that engineering, not a verdict against the approach:

| P0-3 problem | Fix | Ambient pitch intact? |
|---|---|---|
| `HTTP_PROXY` → 405 on plain HTTP | Set `HTTPS_PROXY` only (LLM traffic is all HTTPS) | ✅ yes |
| `SSL_CERT_FILE`/`AWS_CA_BUNDLE` replace trust store → break other HTTPS | Ship a **merged bundle** (system roots + our CA), never CA-only | ✅ yes |
| Daemon = SPOF for all agent traffic | **Always-on dumb shim** → pass-through tunnel when engine down | ✅ yes — *strengthens* it |

None require a wrapper or per-agent config. "Install once, use agents normally" survives every fix.

---

## Recommended v0.1 architecture (MITM-first, hardened)

1. **P0-1 / P0-2:** pure renderer; forward-first streaming. Just implement.
2. **Keep ambient env-var MITM as the default and the pitch.** Hardened per below.
3. **Additive CA bundle** (system roots + our CA) for the replacement-style vars — confirmed-correct, kills the "broke all my HTTPS" bug. For Node agents `NODE_EXTRA_CA_CERTS` is already additive.
4. **HTTPS-only** (drop `HTTP_PROXY`) + verbose `NO_PROXY` (`localhost,127.0.0.1,::1,*.local`).
5. **Always-on dumb shim** owns the proxy port; the smart capture engine sits behind it. Engine down → shim degrades to plain CONNECT pass-through → traffic flows, only capture is lost. Hard SPOF → soft SPOF. **This is the single highest-value guard and directly implements your "in-flight transition proxy" instinct — just as a permanent layer, not a transition-only one.**
6. **launchd `KeepAlive`** (fail-fast, not crash-loop) + **healthcheck-gated env** + a clean **panic/uninstall** (`launchctl unsetenv` + strip `~/.zshenv`) so a user can never get wedged.
7. **`base_url`:** optional escape hatch for pinned runtimes only. Documented, not default.
8. **"Relaunch your agent"** as the propagation UX (reboot = belt-and-suspenders).

**Net:** every P0-3 blast-radius issue is neutralized while the ambient install-once MITM — the product — stays exactly as pitched.

---

## NetworkExtension — the evolution of the pitch (v0.2, paid account in hand)

With David's paid Apple Developer account, NE moves from "defer" to **"the v0.2 capture layer that makes the ambient pitch even purer."**

- **It makes "we don't break anything else" literally true.** NE routes only matching flows (our allowlist) to us; everything else flows normally **by construction** — no global `HTTPS_PROXY`, no `HTTP_PROXY` 405, no `SSL_CERT_FILE` replacement. It **eliminates the entire env-var blast radius** (P0-3a/b) that the v0.1 guards work around. This is a *stronger* version of the ambient pitch, not a different product.
- **It does NOT remove the CA requirement.** To decrypt TLS and read bodies you still need a trusted CA — nothing avoids that if you want body inspection. So NE is about *clean routing*, not *free MITM*. (This was the load-bearing finding, independent of cost.) [Apple: NETransparentProxyProvider]
- **Cost, now that the paid-account gate is gone:** still requires Developer-ID signing + notarization (David can do this), a system-extension bundle inside a signed `.app` in `/Applications`, and a **one-time System Settings "approve system extension" prompt** on first launch (user friction, unavoidable). Harder to debug than a plain proxy. Real work, but no longer blocked.
- **Roadmap fit:** v0.1 ships the hardened env-var MITM (fast, current pitch). v0.2 swaps the capture front-end to `NEAppProxyProvider`/`NETransparentProxyProvider` — same ambient "install once" UX, now with zero env-var collateral. Design the capture engine so the front-end (env-var vs NE) is swappable from day one. Sources: [Apple TN3134], [Apple NETransparentProxyProvider], [Apple NEAppProxyProvider], [ProxyBridge macOS], [codejam mitmproxy-on-macOS].

**The only open call on NE:** v0.1 or v0.2? Recommendation — **v0.2**. The system-extension approval UX + notarization + debugging is enough work that gating the HN launch on it is risky; the hardened env-var MITM delivers the identical pitch now, and NE is a clean, well-scoped follow-up that strengthens it.

**Net:** NetworkExtension is the principled long-term path but is **not** a v0.1 fix and doesn't even remove the CA requirement. It stays on the roadmap, not the launch list.

## Final recommendation (all research folded in)

For **v0.1 launch**, in priority order:
1. **P0-1 pure renderer** + **P0-2 forward-first streaming** — just implement.
2. **Make MITM fail-open** (tunnel on any capture/parse error) — cheapest, highest-value guard; the proxy must never be able to *break* a call, only fail to observe it.
3. **Additive CA bundle** (system roots + our CA) for the replacement-style vars; **drop `HTTP_PROXY`**, HTTPS-only + `NO_PROXY` loopback. — confirmed-correct, removes P0-3a/b.
4. **Health-gated env / watchdog** so trust env is present only while the daemon is up (fail-open against daemon death).
5. **Strongly consider the `base_url`-first pivot** for Claude/Codex/Bedrock — the cleanest long-term story (prior art: claude-meter, LiteLLM, Helicone). Could be v0.1 if scope allows, else v0.2.
6. **Defer NetworkExtension** to the roadmap. **Adopt "takes effect on newly launched / after relogin"** as honest propagation UX, not as a safety mechanism.

This sequence removes every P0/P1 blast-radius issue without requiring the NetworkExtension cost, and leaves a clean upgrade path.
