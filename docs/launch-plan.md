# Agent Observatory — Launch Goals & Plan

> Working doc for the 0→1 HackerNews launch. Companion to `LAUNCH-AUDIT.md` (the full findings).
> Reviewed by hatch codex (gpt-5.5). Status: pre-launch.

## The Goal

Ship a public HackerNews launch of Agent Observatory: a local-first macOS app that makes coding-agent context (instructions, skills, tools, transcript evidence, and verified outbound LLM requests) inspectable. The launch must survive a skeptical, security-savvy audience testing it on their own machines within minutes.

**Definition of "launch-ready":**
1. A new user can download the DMG, open the app, see the demo feed, and enable live capture **without breaking their machine or their coding agents**.
2. Every claim in the README and HN post is literally true.
3. The repo has the standard OSS hygiene HN expects (license, coherent name, green CI).
4. No always-on daemon crash, leak, or cross-origin exposure that becomes the top comment.

## Where We Are

The product is genuinely built and polished: backend builds clean, `go vet` clean, full test suite green, no placeholders/dead UI, real bundled-helper onboarding, consistent `0.1.0` versioning, and an honest README. The core security boundaries hold (no raw prompts persisted, no System-keychain install, provider-host allowlist, system-trust upstream).

The gaps are not depth — they're **blast radius and integration**: the app fights the installed daemon for ports and CA, the proxy breaks streaming for the hosts it inspects, the global env install breaks unrelated traffic, and the always-on daemon has a cross-origin exposure plus a crash race.

## Strategy

**Product thesis (locked):** the ambient, install-once TLS MITM is the product and the pitch — "install once, use your agents normally, we just see the traffic." We are NOT pivoting to base_url (commodity tracing-app pattern; the friction we exist to remove). The blockers below harden the MITM to be *fail-open + minimal footprint*; they do not change the pitch. NetworkExtension is the v0.2 evolution of the same ambient pitch (paid Apple account in hand). See `docs/risky-decisions.md` for the full rationale.

Fix in three waves. **Do not post until Wave 1 is done and re-verified on a clean machine/fake-home.** Wave 2 is strongly recommended pre-post. Wave 3 is launch-day polish and can trail.

---

## Wave 1 — Launch Blockers (must fix before posting)

| # | Task | Files | Done when |
|---|---|---|---|
| 1 | **App becomes a pure renderer.** Probe `GET /healthz` on start; connect to an existing daemon instead of spawning. Self-spawn an engine only when none exists, and put demo on a distinct port (or render synthetic data in-app) so it never collides with the installed daemon's 7878/7879. Never spawn a live engine with an ephemeral CA. | `app/Observatory/EngineClient.swift:108-122`, `ObservatoryApp.swift:5` | Installed user opens app → demo shows synthetic data; switching to Live reads the daemon's real captures; agents' TLS never breaks. |
| 2 | **Stream proxy responses** instead of `io.ReadAll`. Write status+headers, then `io.Copy` the body through; capture is already taken from the request. | `backend/internal/wire/proxy.go:230-236` | Claude/Codex streaming tokens arrive incrementally through the proxy; no full-response stall. |
| 3 | **Shrink the env-install blast radius.** Drop `HTTP_PROXY` (proxy is CONNECT-only → 405s plaintext HTTP); set `HTTPS_PROXY` only; add `NO_PROXY` for `localhost,127.0.0.1,::1,*.local`; make CA trust **additive** — keep `NODE_EXTRA_CA_CERTS` (already additive) but point `SSL_CERT_FILE`/`AWS_CA_BUNDLE`/`REQUESTS_CA_BUNDLE`/`CURL_CA_BUNDLE` at a **merged bundle** (macOS system roots + our CA), never a CA-only file. | `backend/internal/install/install.go:32,188-196`, `backend/internal/wire/ca.go` | After install, `curl http://...` and unrelated HTTPS still work in a new shell; only provider hosts are intercepted. |
| 4 | **Fail-open the MITM (the key guard + your "in-flight transition proxy" idea, as a permanent layer).** Make the proxy tunnel (raw `io.Copy`) on ANY capture/parse error so delivery is never gated on our parsing. Add an always-on dumb shim path so an engine crash degrades to pass-through, not connection failure. Healthcheck-gate the env (only export proxy vars while the daemon answers) + a clean panic/uninstall. | `backend/internal/wire/proxy.go`, `backend/internal/install/install.go` | Killing the capture engine mid-session does NOT break agent calls — traffic flows, only capture pauses. |

**Wave 1 exit gate:** re-run `make qa`, then a manual clean-machine (or fake-home) pass: install → open app → use a real coding agent → confirm capture works, streaming works, unrelated curl/https works, **and killing the daemon mid-call doesn't break the agent** → uninstall → confirm shell restored.

---

## Wave 2 — High (strongly recommend before posting)

| # | Task | Files | Done when | Status |
|---|---|---|---|---|
| 4 | **Lock down CORS.** Drop `Access-Control-Allow-Origin: *`; the native app uses URLSession (no CORS needed). Remove the false "loopback-only keeps this safe" comment. Especially close `/api/explain?path=` to cross-origin. | `backend/cmd/agents/serve.go:86-96` | A web page can no longer `fetch` local sessions/explain from the daemon. | ✅ **Done** — now echoes only localhost/127.0.0.1/::1 origins; confirmed no web client exists in repo. |
| 5 | **Fix send-on-closed-channel race.** Don't `close()` on unsub (let GC reclaim) or gate sends on live membership. | `backend/internal/wire/server.go:71-78,100-115` | SSE disconnect during a capture can't panic the daemon; add a concurrent subscribe/record test. | ✅ **Done** — unsub no longer closes; added `subscribe_test.go` race regression (passes under `-race`). |
| 6 | **Add LICENSE** (MIT or Apache-2.0) at repo root. | `LICENSE` | File present; README links it. | ✅ **Done** — Apache-2.0 (matches David's repo convention), `Copyright 2026 David Rose`. |
| 7 | **Unify the product name** to "Agent Observatory" across README + launch-note. | `README.md:5,79`, `docs/launch-note.md:1` | Docs and product use one name. | ✅ **Done** — README + launch-note now all say "Agent Observatory". |
| 8 | **Confirm the verified tier is actually populated under ambient install** — daemon captures should feed session-detail "Verified evidence," not just the live feed. Fix or scope the claim. | `backend/cmd/agents/monitor.go:70-80`, `backend/cmd/agents/wirewire.go:22-30` | Either verified facts appear in the GUI under the daemon path, or the README scopes the claim. | ⏳ **Open** — needs design; see risky-decisions doc. |

### Also shipped in this pass (Wave 3 easy wins)
- ✅ **Ring buffer** for daemon captures (`maxCaptures = 500`) — bounds memory growth (`server.go`).
- ✅ **Uninstall now removes the whole `StateDir`** (CA + `daemon.log` + `wire-*.json`), so "fully reverses" is literally true (`install.go`).
- ✅ **launch-note caveat** — "newly launched / restart-to-inherit" added.
- ✅ **`agents serve` documented** in the README Commands table.

---

## Wave 3 — Launch-day polish (can trail the post)

| # | Task | Files |
|---|---|---|
| 9 | Cap captures to a ring buffer (daemon memory growth). | `backend/internal/wire/server.go:101` |
| 10 | Soften "Engine unavailable" full-screen on transient startup; add grace/retry. | `app/Observatory/EngineClient.swift:207-214`, `ContentView.swift:66-68` |
| 11 | Make uninstall actually complete (`RemoveAll(StateDir)`) or soften the "fully reverses" doc. | `backend/internal/install/install.go:178` |
| 12 | Validate stable CA on load (expiry/IsCA), regenerate if invalid. | `backend/internal/wire/ca.go:143-158` |
| 13 | Add the "newly launched / env-honoring agents only" caveat to launch-note. | `docs/launch-note.md` |
| 14 | Document `agents serve` in the Commands table. | `README.md` |
| 15 | Drain the spawned helper's stdout/stderr pipes (moot if #1 makes app a pure renderer). | `app/Observatory/EngineClient.swift:119` |
| 16 | Confirm a green CI run exists on `main` so the badge isn't red on launch day. | `.github/workflows/ci.yml` |
| 17 | Optional credibility: `SECURITY.md` (given local-CA/MITM design) + a short demo clip. | new |
| 18 | Cosmetic: onboarding fabricated `max(count,N)` metrics; runtime color/icon coverage; `LSUIElement` decision; CA PEM perm consistency; rune-safe `truncate`. | various (see audit P3) |

---

## Sequencing notes

- Waves 1 and 2 are mostly **independent** and can be parallelized across backend vs app. The one ordering constraint: task #1 (pure renderer) should land before re-testing #2/#3 end-to-end, since the app is how you'll observe them.
- The **single most important fix** (codex's verdict and mine): **#1 — make the app a pure renderer.** It's the difference between "Observatory shows my agents" and "Observatory broke my agents," and it's the path onboarding actively pushes users toward.
- Everything in Wave 3 can ship as a documented known-issue if time-boxed, except #16 (green CI badge), which is free and visible.
