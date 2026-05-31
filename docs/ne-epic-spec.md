# NE Epic — Spec

> NetworkExtension transparent-proxy capture for Agent Observatory (v0.1, locked).
> Companion plan in `ne-epic-plan.md`. Research basis: `architecture-research.md` +
> the NE implementation guide (sourced, in the build agent output).
> Environment verified present: macOS 26.5, Xcode 26.3, XcodeGen 2.45.3 (system-extension
> productType confirmed via probe), Go 1.26.3.

## Goal

Replace the global env-var proxy ingress (`HTTPS_PROXY` + friends) with a macOS
**NETransparentProxyProvider system extension** that routes *only* allowlisted LLM-provider
flows to our existing Go MITM proxy, leaving all other traffic untouched **by construction**.
This makes the ambient pitch ("install once, use your agents normally, we just see the
traffic") literally true with no env-var collateral damage — the same pitch, a cleaner mechanism.

## Architecture (decided)

The NE extension is a **thin L4 relay**, not a TLS terminator. It cannot see inside TLS;
it sees ciphertext. The existing Go proxy keeps 100% of the body-parsing/CA logic.

```
agent → TCP :443 to api.anthropic.com
  → kernel routes flow to NETransparentProxyProvider (includedNetworkRules: all :443)
  → handleNewFlow: peek ClientHello, read SNI
       ├─ SNI not allowlisted → relay DIRECT to real upstream (untouched, no MITM)
       └─ SNI allowlisted → open NWConnection to 127.0.0.1:7879,
            speak `CONNECT api.anthropic.com:443`, then pump ciphertext both ways
  → Go proxy (unchanged) terminates TLS with our CA, parses body, forwards upstream
```

- **Reuses the Go proxy entirely.** It already only accepts `CONNECT` (`proxy.go:67`) and
  already gates which CONNECT targets get MITM'd via `SetInspectHost` (`proxy.go:56`). The NE
  relay just becomes a new *client* of `127.0.0.1:7879`. **Zero Go parser changes.**
- **`handleNewFlow` return-false = kernel pass-through** (transparent-proxy semantics, Apple-documented),
  so non-provider traffic is never touched. Caveat: once we return `true` we've committed to the
  flow; a non-allowlisted SNI discovered after that is relayed direct (not dropped). Start with
  broad `:443` include + SNI filter (the mitmproxy-proven pattern).
- **`NENetworkRule` matches IP/port, not hostname** — so hostname allowlisting happens in
  `handleNewFlow` via SNI parse. Fail-open: if SNI can't be read (e.g. future ECH), pass through.

## ⚠️ The load-bearing decision: how does the CA get *trusted*?

NE changes **routing**, not **trust**. To decrypt bodies the Go proxy still presents a forged
leaf cert, which the agent's TLS client will reject unless it trusts our CA. NE injects no env.
So trust must be delivered one of two ways:

| Option | How | Pro | Con |
|---|---|---|---|
| **A — System keychain CA (recommended for NE)** | Install our root into the login/System keychain as trusted, gated behind the NE system-extension approval the user already grants | True **zero-env-var** ambient capture; this is how Proxyman/Charles/mitmproxy work; matches the "purer pitch" | Broader trust ask; **contradicts the current README line "CA is NOT installed into the System keychain"** — that copy must change for the NE path |
| **B — Keep additive per-process env for trust only** | NE does routing; still inject `NODE_EXTRA_CA_CERTS` (additive) / merged-bundle for trust | Keeps CA out of system keychain | Defeats much of the point — if we inject env anyway we barely need NE; still per-process, still misses GUI apps |

**Decision: Option A**, scoped and honest. The system extension is signed, notarized, and
**explicitly user-approved in System Settings** — installing a trusted root behind that gate is
defensible and is the standard for this class of tool. The README/trust-model copy gets rewritten
for the NE path (this is itself a spec deliverable). Option B remains the fallback if keychain
trust proves problematic in testing. This is flagged prominently for David; building proceeds on A.

## Scope

### In scope (v0.1)
- New `system-extension` target `TransparentProxyExtension` (Swift): provider + SNI parser + relay pump + settings.
- Host-app activation controller (`OSSystemExtensionRequest` + `NETransparentProxyManager`).
- Entitlements for both targets (networkextension + system-extension.install + app group).
- `project.yml` wiring (two targets, embed, manual signing scaffolding).
- CA trust delivery (Option A) + uninstall that removes it.
- Onboarding/README copy rewrite for the NE trust model.
- Keep the env-var path as a documented fallback flag (don't delete it).

### Out of scope (v0.1)
- UDP/QUIC flows (TCP :443 only; HTTP/3 to providers is rare and falls back to TCP).
- Per-app filtering by `sourceAppAuditToken` (available, but not needed for v0.1).
- Replacing the Go proxy (it stays as the TLS-terminating body parser).

## Acceptance criteria

**Headless-verifiable now (this session):**
1. `xcodegen generate` produces a valid project with both targets (system-extension productType).
2. `xcodebuild -scheme Observatory build` compiles the extension + app (logic, pump, SNI parser).
3. Unit tests pass: SNI-from-ClientHello parser, allowlist suffix matcher.
4. Go test: a raw client doing `CONNECT api.anthropic.com:443` through `127.0.0.1:proxy` (the NE
   relay's exact behavior) yields a parsed capture — proves the integration contract end-to-end on
   the Go side. (Extends existing `proxy_test.go`.)
5. `make backend-qa` stays green; existing app still builds.

**Requires David (interactive, can't be headless) — teed up, not faked:**
6. App IDs + Developer ID provisioning profiles with the NE entitlement (Apple portal).
7. Developer-ID sign + notarize; app in `/Applications`.
8. First-launch system-extension approval in System Settings.
9. Live end-to-end: run a real agent, see a verified capture, confirm unrelated traffic untouched,
   uninstall cleanly (sysext deactivated + CA trust removed).

## Risks
- **SNI commitment** (return-true-then-direct-relay for non-allowlisted): mitigated by broad-include
  + early false; documented.
- **Keychain trust UX** (Option A): the trust install may itself prompt; sequence it right after the
  approved sysext activation so it reads as one consented step.
- **Signing/notarization friction**: the largest unknown; fully gated to David's interactive steps.
- **ECH** disabling SNI visibility: fail-open to pass-through; revisit if providers enable it.
