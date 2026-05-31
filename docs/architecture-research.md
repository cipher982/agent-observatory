# Agent Observatory — Safest Architecture Research (sourced)

> Deep web research, 2026-05-31, to isolate the safest path past the P0-3 blast-radius
> blockers. All findings cited to vendor docs / RFCs / Apple developer docs. Companion to
> `docs/risky-decisions.md` (which holds the decision framing); this file is the evidence base.

## Headline

Agent Observatory's **global env-var MITM is the most fragile point on the LLM-observability spectrum.** Two of the three launch fears (breaking unrelated traffic; daemon SPOF) are largely *eliminable*; the prior-art finding suggests an even safer default (per-agent `base_url`).

---

## Q1 — Selective MITM without collateral damage

Every production tool (mitmproxy, Proxyman, Charles, Burp, HTTP Toolkit) uses the same shape: a CONNECT forward proxy that decides **per-connection at CONNECT time (or by sniffing TLS ClientHello SNI in transparent mode)** — allowlist match → forge leaf cert + terminate; else → `200 Connection Established` + blind byte relay. Tunneled connections never see a forged cert and don't depend on the CA at all.

- mitmproxy `--allow-hosts` / `ignore_hosts`; Proxyman SSL list include/exclude; Charles "identify host names to enable SSL Proxying"; Burp "TLS pass through."
- **Trust is NOT host-scoped:** a trusted CA vouches for *any* hostname. You can't "trust our CA only for api.openai.com" via the trust store. Scope comes from *which connections* you MITM (the allowlist) or *which client* trusts the CA (HTTP Toolkit's dedicated-browser model).

Sources: docs.mitmproxy.org/stable/howto/ignore-domains/ · /concepts/how-mitmproxy-works/ · /concepts/certificates/ · charlesproxy.com/documentation/proxying/ssl-proxying/ · portswigger.net/burp/documentation/desktop/settings/tools/proxy · docs.proxyman.com/basic-features/ssl-proxying · httptoolkit.com/docs/reference/intercept-page/

**Implication:** our allowlist-tunnel design is correct and standard for *connection scoping*. Safety must come from the tiny allowlist + additive CA injection (Q2), not host-scoped trust.

---

## Q2 — CA trust env vars: additive vs replacement (CONFIRMED)

| Env var | Behavior | Source |
|---|---|---|
| `NODE_EXTRA_CA_CERTS` | **ADDITIVE** — roots "extended with extra certificates" | nodejs.org/api/cli.html |
| `SSL_CERT_FILE` | **REPLACES** default CA file | docs.openssl.org/3.3/man3/SSL_CTX_load_verify_locations/ |
| `SSL_CERT_DIR` | **REPLACES** dir, unless a delimiter list includes the system dir | docs.openssl.org/master/man3/X509_get_default_cert_file/ |
| `AWS_CA_BUNDLE` | **REPLACES**; no additive mode | docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html |
| `REQUESTS_CA_BUNDLE` | **REPLACES** certifi default | docs.python-requests.org |
| `CURL_CA_BUNDLE` | **REPLACES** curl default | curl.se/docs/manpage.html |

Pointing the REPLACE-style vars at a **CA-only file breaks all other HTTPS** for those processes — launch-blocking. Additive options: Node is genuinely additive; OpenSSL `SSL_CERT_DIR` can be union-like via a list including the system hashed dir; **AWS/requests/curl have no additive var → must concatenate (system roots + our CA) into one merged bundle** and point the var at *that*, regenerated when the system bundle updates.

**Implication:** Node agents → set only `NODE_EXTRA_CA_CERTS`. REPLACE-style vars → only ever a *merged* bundle, never CA-only. Merged-bundle generation is a required installer step.

---

## Q3 — HTTP_PROXY and plaintext HTTP

`HTTP_PROXY` + a plain `http://` URL → client sends RFC 7230 **absolute-form** (`GET http://host/path`), not CONNECT. A CONNECT-only proxy returning 405 is non-compliant and breaks all plain-HTTP. Two fixes: implement absolute-form forwarding, **or (production-standard) set only `HTTPS_PROXY`, never `HTTP_PROXY`** — all LLM traffic is HTTPS/CONNECT, so this sidesteps it.

`NO_PROXY` is dangerously non-portable: leading-dot semantics differ (Go vs requests); `NO_PROXY=*` works everywhere except Python requests; wildcards Node-only; CIDR Go/curl/requests-ish; **only Go auto-bypasses localhost** — everyone else needs `localhost,127.0.0.1,::1` explicit; curl honors only lowercase `http_proxy`.

Sources: rfc-editor.org/info/rfc7230 · pkg.go.dev/golang.org/x/net/http/httpproxy · undici EnvHttpProxyAgent docs · docs.python.org urllib.request · everything.curl.dev/usingcurl/proxies/env.html

**Implication:** set only `HTTPS_PROXY`; set `NO_PROXY=localhost,127.0.0.1,::1,*.local` defensively; assume lowest-common-denominator NO_PROXY (plain suffix + explicit localhost).

---

## Q4 — Fail-open / the always-on daemon SPOF

`HTTPS_PROXY=localhost:PORT` is fail-*closed*: daemon down → every new proxy-aware connection fails. Mitigations ranked:

1. **Dumb always-on shim on the port (best for env-var proxying).** A tiny CONNECT-tunneling proxy permanently owns the port; the smart capture engine sits behind it. Engine crash → shim degrades to pass-through → traffic flows, capture lost. CLIs honoring `HTTPS_PROXY` have **no DIRECT fallback**, so this — not PAC — is what protects agent CLIs.
2. **PAC + `DIRECT` fallback** — browsers/system-proxy apps fall through to DIRECT (Chrome marks dead proxy bad ~5min); **most agent CLIs ignore PAC.** Bonus, not mechanism.
3. **launchd `KeepAlive`** — fast restart but doesn't remove the SPOF window; must fail-fast (launchd throttles crash-loops).
4. **Watchdog unsetting env** — only affects future processes; weak.

Comparable tools: Proxyman's helper "gracefully reverts the HTTP Proxy Config if Proxyman crashed." Little Snitch/LuLu are NetworkExtension *content filters*, not proxies — they never insert an env proxy hop, so can't wedge connectivity (fail-open for availability). `pf`/NE transparent proxies are fail-*closed* if the target dies — need their own health-monitor.

Sources: developer.mozilla.org PAC file · chromium proxy.md · developer.apple.com CFNetworkCopyProxiesForURL · launchd.plist(5) · docs.proxyman.com proxy-setting-tool

**Implication:** the dumb always-on shim is the single biggest robustness win + launchd KeepAlive. PAC+DIRECT is a browser-only extra.

---

## Q5 — NetworkExtension vs env-var proxy

NE (NETransparentProxyProvider / NEAppProxyProvider / NEFilterDataProvider) intercepts system-wide/per-app without env vars/PAC, **but:**
- Entitlement `com.apple.developer.networking.networkextension`; for Developer-ID: `-systemextension` variants + `com.apple.developer.system-extension.install`, system-extension bundle inside a **signed .app in /Applications**, **Developer ID signing + notarization**, **paid Apple Developer Program**, and a **System Settings system-extension approval** on first launch.
- **Critically: NE does NOT remove the trust-store requirement.** No Apple API decrypts TLS without a trusted CA. NE changes *how you capture flows*, not *whether you need a trusted CA to decrypt* — so its headline benefit doesn't apply to our core problem.
- Indie precedent: ProxyBridge, BaoLianDeng use NETransparentProxyProvider.

Sources: developer.apple.com entitlements/...networkextension · NETransparentProxyProvider · TN3134 · neappproxyprovider · github.com/InterceptSuite/ProxyBridge · codejam.info/2021/07/intercept-macos-app-traffic-mitmproxy.html

**Implication:** right long-term for coverage + clean uninstall, but large cost for v0.1 while not solving the CA problem. Stay env-var-based; design the front-end (env vs NEAppProxyProvider) swappable.

---

## Q6 — Env propagation & "install on reboot"

Env captured at exec, never refreshes. `launchctl setenv` reaches future launchd/GUI-launched apps **on next launch**, is **not persistent across reboot/logout** (needs a login LaunchAgent to re-run). `~/.zshenv` reaches shells/CLIs, not Dock-launched GUI apps. Old `environment.plist`/`launchd.conf` are dead.

**Reboot is mostly conservative, not required for *new* processes:** new GUI app → `launchctl setenv` + relaunch the app; new terminal → new shell; already-running daemons → must be individually restarted. Reboot is the crude way to force the whole graph to restart cleanly.

Sources: launchd.info · launchctl(1) · zsh intro · Apple QA1067

**Implication:** ship a login LaunchAgent (`launchctl setenv`, GUI) + `~/.zshenv` (terminals); default UX = "quit & relaunch your agent/IDE," reboot = belt-and-suspenders.

---

## Q7 — Prior art / first principles

Four capture patterns, least→most fragile:
1. **SDK wrapper** (Langfuse `from langfuse.openai import openai`) — zero network risk, needs code ownership.
2. **OpenTelemetry instrumentation** (OpenLLMetry/Traceloop) — low risk, needs runtime hooks.
3. **Local proxy via `base_url` override** (LiteLLM `:4000`, Portkey `:8787/v1`, Helicone) — **dominant pattern for dev-machine agent observability; no cert, no MITM, low breakage.** **`claude-meter` is direct prior art**: local proxy between Claude Code and Anthropic via `ANTHROPIC_BASE_URL`, captures full raw bodies locally, forwards unchanged.
4. **True TLS MITM** (mitmproxy regular/transparent/Local-Capture/WireGuard) — only when the agent can't be re-pointed; still needs trusted CA + SSE handling.

Sources: github.com/abhishekray07/claude-meter · docs.litellm.ai · github.com/Helicone/ai-gateway · portkey docs · github.com/traceloop/openllmetry · langfuse.com/docs/get-started · docs.mitmproxy.org/stable/concepts/modes/

**Implication:** the ecosystem's "doesn't break traffic" answer is **`base_url` override, not system-wide MITM.** Claude Code, Codex, Aider, Continue all support base_url. Global MITM is justified only for agents we can't reconfigure.

---

## Recommended safest architecture (synthesis)

**(a) Minimal safe env set:** `HTTPS_PROXY` only (never `HTTP_PROXY`); `NODE_EXTRA_CA_CERTS` (additive) for Node; `NO_PROXY=localhost,127.0.0.1,::1,*.local`; REPLACE-style CA vars only if pointed at a **merged** bundle, ideally only when a Bedrock/Python/curl agent is in use.

**(b) Additive CA:** Node done; for AWS/requests/curl generate a merged PEM (system roots + our CA), regenerate on system-bundle update. CA-only file = the launch-blocking bug.

**(c) Env vs NE:** keep env-var proxying for v0.1 (NE doesn't remove the CA need and adds Developer-ID/notarization/approval cost); make the capture front-end swappable; **strongly consider per-agent `base_url` as the default mode** for agents that support it.

**(d) Reboot vs transition proxy:** neither as headline. Reboot is conservative; the "in-flight transition proxy" instinct → ship it as the **permanent dumb shim** (e1), not a transition-only thing. Default UX = "relaunch your agent."

**(e) Cheapest robust guards for v0.1:**
1. Always-on dumb shim → pass-through when engine down (hard SPOF → soft).
2. launchd `KeepAlive` (fail-fast).
3. Merged CA bundle generator for REPLACE-style vars.
4. `HTTPS_PROXY` only; verbose `NO_PROXY`.
5. Healthcheck-gated env injection + clean panic/uninstall (`launchctl unsetenv` + strip `~/.zshenv`).
6. Keep the allowlist tiny (already done).

**Biggest single risk reduction:** default to per-agent `base_url` where supported; fall back to the (now shimmed, fail-open) global MITM only for agents that don't. This neutralizes all three launch fears for the common case.
