# NE Epic — Verification Handoff

> Everything headless-verifiable is **done and green** (below). The remaining steps
> require Apple Developer portal access, code-signing, notarization, and an interactive
> System Settings approval — they **cannot** be done headlessly and are NOT faked.
> This is your runbook to take the NE system extension live.

## What's already built & verified (green, on branch `ne-epic`)

| Verification | Result |
|---|---|
| `xcodegen generate` with app + `system-extension` + test targets | ✅ valid project |
| `xcodebuild -scheme Observatory build` (app + embedded sysext) | ✅ **BUILD SUCCEEDED** |
| Sysext embedded at `…app/Contents/Library/SystemExtensions/TransparentProxyExtension.systemextension` with correct `NSExtensionPointIdentifier` + `NEProviderClasses` | ✅ verified in built bundle |
| `xcodebuild -scheme ObservatoryTests test` (SNI parser + allowlist) | ✅ **6/6 passed** |
| Go `TestNERelayContract` — drives the exact raw `CONNECT` byte sequence the NE relay emits, asserts TLS termination + body parse + byte-identical forward | ✅ pass under `-race` |
| `make backend-qa` (full Go suite) | ✅ green |
| `agents trust install|remove|status` subcommand | ✅ builds, smoke-tested |

The code compiles and the integration contract is proven end-to-end on the Go side. What
remains is signing/provisioning/approval — Apple-gated, interactive.

## Architecture recap (what you're shipping)

NE system extension = thin L4 relay. It routes only allowlisted `:443` flows (SNI-matched to
OpenAI/Anthropic/Bedrock) to the local Go MITM proxy on `127.0.0.1:7879` via HTTP `CONNECT`;
everything else passes through untouched. The Go proxy (unchanged) terminates TLS with the
local CA and parses bodies. **No global env-var hijack.** CA trust is delivered to the login
keychain behind the approved sysext (`agents trust install`).

## Portal registration — DONE (automated via browser-harness, 2026-05-31)

All three identifiers are registered on the portal (Team `M49WM6JSW8`, Individual):
- ✅ App Group `group.com.github.cipher982.agentobservatory`
- ✅ App ID `com.github.cipher982.agentobservatory.Observatory` — App Groups + Network Extensions + System Extension
- ✅ App ID `com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension` — App Groups + Network Extensions

Team ID `M49WM6JSW8` is wired into `app/project.yml` (`DEVELOPMENT_TEAM`).

## The one true remaining blocker: a Developer ID certificate

Your Mac has an **Apple Development** cert but **no Developer ID Application** cert
(`security find-identity -p codesigning` → 0 Developer ID). A signed/notarized build needs it,
and creating it is the irreducible manual step: the private key must be generated in *your*
login keychain.

### Step 0 — Create a Developer ID Application certificate (Keychain Access, ~3 min) — YOU
- Keychain Access → Certificate Assistant → **Request a Certificate from a Certificate
  Authority** → "Saved to disk" → save the `.certSigningRequest` (this generates your key locally).
- developer.apple.com → Certificates → **+** → **Developer ID Application** → upload the CSR →
  download → double-click to install. Then tell me — I can drive provisioning-profile creation
  on the portal from there; signing config in `project.yml` is already wired.

### Your remaining interactive steps

### 2. Signing config (project.yml)
Both targets are set up for Developer ID manual signing in `app/project.yml`.
For a different Apple team or profile set, update each target's signing block:
```yaml
        CODE_SIGN_STYLE: Manual
        CODE_SIGN_IDENTITY: "Developer ID Application"
        DEVELOPMENT_TEAM: <YOUR_TEAM_ID>
        PROVISIONING_PROFILE_SPECIFIER: "<that target's profile name>"
```
Then `cd app && xcodegen generate`.

### 3. Build, sign, notarize
```bash
make release
NOTARY_PROFILE="<your notarytool profile>" make notarize
make release-qa
```
The default release path is headless and does not run Finder AppleScript.
Hardened runtime is already on (`ENABLE_HARDENED_RUNTIME: YES`).

### 4. Install & approve
- Copy `Agent Observatory.app` to **`/Applications`** (sysext activation requires this).
- Launch it; trigger activation (onboarding "Enable live capture" → `ProxyController.activate()`).
- macOS shows a **System Settings → General → Login Items & Extensions** approval prompt.
  Approve the system extension. (`ProxyController` surfaces `.needsApproval` for this.)
- The Security framework will then prompt once to authorize the **login-keychain CA trust**
  (`agents trust install`). Approve it.

### 5. Live verification
```bash
# confirm the sysext is activated:
systemextensionsctl list
# run a real agent (new shell / relaunched), then check the app's live feed +
# session detail for a VERIFIED capture.

# confirm unrelated traffic is UNTOUCHED (the whole point):
curl -v https://example.com >/dev/null     # should succeed normally, no MITM
curl -v http://neverssl.com >/dev/null      # plain HTTP unaffected
```
Expected: provider calls captured; `example.com`/plain-HTTP completely unaffected (passed
through by `handleNewFlow` returning the direct-relay path).

### 6. Uninstall (clean reversal)
- App "disable live capture" → `ProxyController.deactivate()` deactivates the sysext + removes
  keychain CA trust + stops the tunnel.
- `agents uninstall` also removes keychain trust (calls `trust remove`) and the state dir.
- Verify: `systemextensionsctl list` shows it gone; `curl https://api.anthropic.com` no longer
  routed; no Observatory cert left trusted in Keychain Access.

### Debugging aids
- `systemextensionsctl list` — activation state.
- `log stream --predicate 'subsystem == "com.github.cipher982.agentobservatory.ext"'` — provider logs.
- During dev only: `systemextensionsctl developer on` (SIP considerations apply).

## Known caveats baked into the build
- **Deprecated APIs (macOS 15+):** `NWHostEndpoint` and the IP-based `NENetworkRule` init are
  deprecated but still functional on macOS 26 and remain the way to express CIDR flow rules.
  Compiles with warnings, not errors. Revisit if Apple removes them.
- **SNI commitment:** once `handleNewFlow` returns `true` we own the flow; a non-allowlisted SNI
  discovered after the peek is relayed direct (not dropped). This is the mitmproxy-standard
  pattern. Only `:443` flows are ever taken; loopback/RFC1918 are excluded at the kernel rule.
- **ECH:** if a provider enables Encrypted ClientHello, SNI becomes unreadable → we fail open
  (direct relay). Capture would silently stop for that provider; revisit if it happens.
- **Trust model copy:** the README "Trust Model" section is being updated for the NE path
  (CA in login keychain behind sysext approval, no env hijack). The old "never touches the
  keychain" line applies to the legacy env-var path, which remains available as a fallback.
