# NetworkExtension Reset And Live-Capture Proof Runbook

Use this when the Observatory system-extension state has version churn, stale
approval prompts, or old extensions waiting to uninstall on reboot.

This runbook is intentionally explicit because the final v0.2 proof depends on
macOS state, not just code.

## Preconditions

- Fresh artifacts exist under `dist/` from current `HEAD`.
- Public release artifacts have been notarized/stapled if this is a release
  validation pass.
- No unrelated high-value agent run is in progress.
- You are ready for macOS approval prompts.

## 1. Capture Starting State

```bash
git rev-parse HEAD
git status --short --branch
systemextensionsctl list | grep agentobservatory || true
"/Applications/Agent Observatory.app/Contents/Resources/agents" status || true
security find-certificate -a -c "Agent Observatory Local CA" -Z \
  ~/Library/Keychains/login.keychain-db || true
```

If old Observatory extensions show `waiting to uninstall on reboot`, reboot
before continuing. Do not try to prove v0.2 readiness from a dirty extension
registry.

Developer cleanup commands do not avoid this requirement on a normal SIP-enabled
Mac. `systemextensionsctl gc` only removes orphaned extensions and does not
clear a valid `terminated waiting to uninstall on reboot` entry.
`systemextensionsctl uninstall <teamID> <bundleID>` is blocked while System
Integrity Protection is enabled. Do not use `systemextensionsctl reset` for
release validation; it clears all system-extension state, including unrelated
NetworkExtensions such as VPN tools.

## 2. Replace The Installed App

Quit Agent Observatory completely, then replace the app from the fresh artifact:

```bash
rm -rf "/Applications/Agent Observatory.app"
cp -R "dist/Agent Observatory.app" /Applications/
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister \
  -f -R -trusted "/Applications/Agent Observatory.app"
spctl -a -vvv "/Applications/Agent Observatory.app"
xcrun stapler validate "/Applications/Agent Observatory.app"
```

For local non-notarized dev builds, `spctl`/`stapler` are expected to fail. For a
v0.2 release gate, they must pass.

## 3. Install The Daemon And Stable CA

```bash
"/Applications/Agent Observatory.app/Contents/Resources/agents" install
"/Applications/Agent Observatory.app/Contents/Resources/agents" status
"/Applications/Agent Observatory.app/Contents/Resources/agents" trust status
launchctl print "gui/$(id -u)/com.github.cipher982.agentobservatory"
curl -fsS http://127.0.0.1:7878/healthz
```

Expected:

- status reports `overall: installed`;
- launchd service exists;
- `/healthz` returns `ok: true`;
- trust status reports the current local CA is trusted in the login keychain
  after the explicit trust step below;
- no proxy env vars such as `HTTPS_PROXY`/`HTTP_PROXY` are installed by
  Observatory.

When ready for the macOS Security authorization prompt:

```bash
"/Applications/Agent Observatory.app/Contents/Resources/agents" trust install
```

## 4. Enable The System Extension

Open Agent Observatory from `/Applications`, use onboarding/menu to enable live
capture, and approve the NetworkExtension in System Settings when prompted.

After approval:

```bash
systemextensionsctl list | grep agentobservatory
curl -fsS http://127.0.0.1:7878/healthz
security find-certificate -a -c "Agent Observatory Local CA" -Z \
  ~/Library/Keychains/login.keychain-db
```

Expected:

- exactly the current Observatory extension is `[activated enabled]`;
- no older Observatory extension remains active;
- the local CA is trusted in the login keychain.

## 5. Prove Unrelated Traffic Is Untouched

```bash
curl -fsSI https://example.com
curl -fsSI http://example.com
curl -vkI https://example.com 2>&1 | grep -E "issuer|subject"
```

Expected:

- HTTPS and HTTP succeed;
- `example.com` presents its real certificate chain, not Agent Observatory Local
  CA;
- the live feed does not emit a capture for unrelated hosts.

## 6. Prove Real Claude Code Capture And Completion

Start a new shell after install so additive trust env vars are inherited. First
prove Claude Code can complete without Observatory being the suspect:

```bash
env | awk -F= '/(ANTHROPIC|CLAUDE|API_KEY|TOKEN)/ {print $1}' | sort
claude auth status
claude -p "Say exactly: observatory auth preflight"
```

If `claude auth status` says logged in but the prompt returns `401 Invalid
authentication credentials`, repair Claude Code auth before testing capture:

```bash
claude auth logout
claude auth login
claude -p "Say exactly: observatory auth preflight"
```

Do not use `claude --bare` for this proof; bare mode skips OAuth/keychain auth
and would test a different credential path than normal Claude Code usage.

Then run a minimal real Claude Code turn while watching the Observatory stream.

In one terminal:

```bash
curl -N http://127.0.0.1:7878/api/stream
```

In a newly launched shell:

```bash
env | grep -E 'NODE_EXTRA_CA_CERTS|CODEX_CA_CERTIFICATE'
claude -p "Say 'observatory live capture proof' and do not use tools."
```

Expected:

- Claude Code completes normally;
- `/api/stream` emits a capture for `api.anthropic.com` or the configured
  Bedrock runtime;
- the event has the expected runtime, endpoint, prompt length, and tool count;
- `/healthz` does not show new `clientTLSFailures`.

Record the exact prompt, completion output, and capture JSON in
`docs/launch-readiness.md`.

## 7. Prove Disable And Uninstall

Disable live capture from the Agent Observatory menu bar, then:

```bash
systemextensionsctl list | grep agentobservatory || true
"/Applications/Agent Observatory.app/Contents/Resources/agents" uninstall
"/Applications/Agent Observatory.app/Contents/Resources/agents" status || true
security find-certificate -a -c "Agent Observatory Local CA" -Z \
  ~/Library/Keychains/login.keychain-db || true
curl -fsSI https://api.anthropic.com || true
```

Expected:

- system extension is inactive or pending uninstall, not actively routing;
- daemon/profile/state are removed;
- Observatory CA trust is gone;
- provider traffic reaches the real network path.

## Failure Handling

- If the extension remains `waiting for user`, approve it in System Settings or
  reboot if macOS says activation will complete after reboot.
- If an older Observatory extension remains `waiting to uninstall on reboot`,
  reboot before claiming clean state.
- If `/healthz` reports `clientTLSFailures`, restart the agent shell/process so
  it inherits the additive trust env, or disable capture.
- If the agent gets 502, check daemon logs for an upstream loop or TLS failure
  before retrying.
