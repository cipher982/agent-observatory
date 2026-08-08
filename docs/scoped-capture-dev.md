# Scoped capture (dev-only safe iteration)

> How to iterate on the NE transparent proxy on the real Mac WITHOUT risking the
> host's own agents (Codex/Claude/browser). Mirrors mitmproxy's macOS local mode,
> which narrows interception to specific apps/processes to limit blast radius.

## The safety property (load-bearing)

The extension's `handleNewFlow` already returns `false` (pass-through) for any
flow it doesn't want. We add a **dev-scope allowlist** that can ONLY NARROW what
gets intercepted — never widen it:

- **Production (no dev-scope file):** unchanged — intercept allowlisted provider
  `:443` flows from any app.
- **Dev-scope active (file present):** intercept ONLY flows whose source app
  signing identifier is in the dev allowlist AND would otherwise be eligible.
  Every other app (Codex, Claude, Slack, browser) is passed through untouched.

Because dev-scope is intersected with the existing eligibility, the worst case if
the file is mistakenly present is *less* interception, never more. It cannot make
the extension capture something it otherwise wouldn't.

## Mechanism

- Toggle: a marker file at `/tmp/agent-observatory-dev-scope`. If it exists, each
  non-empty, non-`#` line is an allowed source-app signing identifier. Read once
  per tunnel start (toggle by disabling/enabling capture via the menu kill switch).
- `handleNewFlow`: after the existing self-bypass and `:443` checks, if dev-scope
  is active and the flow's `sourceAppSigningIdentifier` is NOT in the allowlist,
  return `false` (pass-through). Otherwise proceed as normal.
- Dev logging: in dev-scope mode, log every flow's source id + decision, so we can
  confirm the harness's identifier matches.

## The dev harness

A tiny signed Go binary (`devharness`) that makes one real provider HTTPS request
trusting the local CA — standing in for a configured agent. Signed ad-hoc with a
STABLE identifier `com.github.cipher982.agentobservatory.devharness` so the
extension can allowlist it. Built by `make devharness`.

## Proof plan (safe; only the harness is ever intercepted)

1. Build + install the scoped extension; approve once (safe — scoped).
2. `echo com.github.cipher982.agentobservatory.devharness > /tmp/agent-observatory-dev-scope`
3. Enable capture (menu kill switch). Confirm your other agents still work
   (api.openai.com still shows its REAL cert — only the harness is scoped in).
4. Run `devharness https://api.openai.com/v1/models`:
   - expect a REAL provider response (HTTP 401), NOT 502 → proves the routing
     loop is fixed (the proxy's own upstream dial reaches the real provider).
   - confirm a capture appears for it.
   - daemon log shows NO `x509: unknown authority` loop.
5. Remove the file + disable capture when done.

## Replacing the extension during iteration

The extension only swaps on an `activate()` that uses `.replace`, so a plain
rebuild-and-install does not take effect.

- **Bump `CURRENT_PROJECT_VERSION` or macOS will not replace it.** It needs a
  distinct version to consider the new bundle newer. This is the single most
  common reason a "fixed" extension still shows old behavior.
- Drive the replace headlessly through the menu-bar kill switch rather than
  clicking: AppleScript `click menu bar item 1 of menu bar 2`, then "Disable Live
  Capture" / "Enable Live Capture". This swapped v1 to v3 with no re-approval
  prompt, because the team and identifier are unchanged.

## A capture event does not prove the forward succeeded

An entry appearing in the SSE feed only means the proxy saw the flow. It says
nothing about whether the request reached the provider. Always confirm the client
got a real upstream response (a provider 401 or 405), not a 502. A curl-with-CA
test masked exactly this: the event appeared while the agent's request was
looping back and never leaving the machine.
