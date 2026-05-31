# NE Epic — Plan

> Build sequence for `ne-epic-spec.md`. Each step has a verification gate.

## File / target layout to create

```
app/
  project.yml                              # MODIFY: add extension target + dependency + manual-signing scaffold
  Observatory/
    Observatory.entitlements               # NEW: system-extension.install + networkextension + app-group
    ProxyController.swift                   # NEW: OSSystemExtensionRequest + NETransparentProxyManager
  TransparentProxyExtension/               # NEW target
    main.swift                             # NEProvider.startSystemExtensionMode()
    TransparentProxyProvider.swift         # handleNewFlow + relay pump (flow ⇄ Go proxy CONNECT)
    ProxySettings.swift                    # includedNetworkRules (all :443) + excludes (RFC1918/loopback)
    SNI.swift                              # ClientHello SNI parser (pure, unit-tested)
    Allowlist.swift                        # suffix matcher (pure, unit-tested)
    Info.plist                             # NSExtension keys (app-proxy point, principal class)
    TransparentProxyExtension.entitlements # networkextension + app-group
  ObservatoryTests/                        # NEW (or extend): SNI + allowlist unit tests
backend/                                   # MOSTLY UNCHANGED
  internal/wire/proxy_test.go              # EXTEND: raw-CONNECT-client test mirroring the NE relay
  cmd/agents/*                             # behind a flag: NE mode skips env injection
docs/                                      # spec + plan + trust-model copy rewrite
```

## Steps (with gates)

### Phase 0 — Branch + scaffold
- Create branch `ne-epic`.
- **Gate:** clean tree, on branch.

### Phase 1 — Pure, testable logic first (highest-confidence, fully headless)
1. `SNI.swift` — parse SNI from a TLS ClientHello byte buffer (handle: not-TLS → nil; no-SNI → nil; fragmented record → need-more).
2. `Allowlist.swift` — suffix match (`host == s || host.hasSuffix("."+s)`) over `["api.openai.com","api.anthropic.com","amazonaws.com"]`.
3. Unit tests for both (real ClientHello fixtures incl. a Bedrock/OpenAI/Anthropic SNI + a non-match + a truncated buffer).
- **Gate:** `xcodebuild test` (or `swift test`) green for these units.

### Phase 2 — Go integration contract (headless, proves the whole pipe on the Go side)
4. Extend `proxy_test.go`: a raw TCP client opens `CONNECT api.anthropic.com:443` to the proxy, completes TLS as if it were the agent (trusting the proxy CA), sends a real Anthropic Messages body, asserts a `Capture` with the right system prompt + tools — i.e. exactly what the NE relay will drive.
5. Confirm `SetInspectHost` allowlists the three provider host patterns in the daemon path.
- **Gate:** `make backend-qa` + the new test green under `-race`.

### Phase 3 — Extension target (compiles headless; runs only after signing)
6. `TransparentProxyProvider.swift` — `startProxy` (setTunnelNetworkSettings), `handleNewFlow` (peek→SNI→allow?→relayViaGoProxy / relayDirect), `pump` (flow ⇄ NWConnection), `stopProxy`.
7. `ProxySettings.swift`, `main.swift`, `Info.plist`, extension entitlements.
- **Gate:** target compiles via `xcodebuild build`.

### Phase 4 — Host activation + project wiring
8. `ProxyController.swift` — activation request + delegate + `NETransparentProxyManager` save/start.
9. `Observatory.entitlements`; `project.yml` — add target, `dependencies: [{target: ..., embed: true}]`, manual-signing settings (left with placeholder profile specifiers David fills in).
10. Wire a UI affordance in onboarding to trigger activation (reuse existing onboarding panel).
- **Gate:** `xcodegen generate` valid; full `xcodebuild -scheme Observatory build` compiles app + embedded sysext.

### Phase 5 — CA trust delivery (Option A) + uninstall
11. Install our root into the login keychain as trusted at activation time (gated behind the approved sysext); `agents uninstall` (and a panic path) removes both the sysext activation and the keychain trust.
12. Behind a mode flag, the Go run/daemon path **skips env injection** when NE mode is active.
- **Gate:** uninstall verified to remove trust (headless keychain check where possible).

### Phase 6 — Docs / trust-model rewrite
13. Rewrite README "Trust Model" + onboarding copy for the NE path (CA-in-keychain-behind-approval, no env hijack). Update `risky-decisions.md` to mark NE as the shipped v0.1 ingress.
- **Gate:** docs consistent; no contradictory "never touches keychain" line on the NE path.

### Phase 7 — Verification handoff
14. Write the exact interactive steps for David: portal App IDs + profiles, sign, notarize, `/Applications`, approve, live test, uninstall. Provide `systemextensionsctl list` / `developer on` debug commands.
- **Gate:** a runnable checklist; everything headless-verifiable is green and documented.

## Verification matrix
| Item | Headless now | Needs David |
|---|---|---|
| SNI parser, allowlist unit tests | ✅ | |
| Go CONNECT-relay contract test | ✅ | |
| Extension + app compile | ✅ | |
| `xcodegen generate` both targets | ✅ | |
| Provisioning profiles w/ NE entitlement | | ✅ portal |
| Sign + notarize + /Applications | | ✅ |
| System-extension approval | | ✅ System Settings |
| Live capture + pass-through + uninstall | | ✅ |

## Rollback / fallback
- The env-var path stays in the tree behind a flag. If keychain trust or signing blocks the NE path,
  v0.1 can still ship the hardened env-var MITM (the other goal's fixes). NE is additive, not a bridge-burn.
