# Onboarding redesign + NE-activation fix — spec

> Working spec for review. Two problems surfaced during live validation:
> (1) the system-extension activation errored "Extension not found in App
> bundle"; (2) the onboarding is too busy and the actual activation controls sit
> below the fold, so a first-time user never sees them.

## Problem 1 — "Unable to find any matched extension"

`ProxyController.activate()` calls
`OSSystemExtensionRequest.activationRequest(forExtensionWithIdentifier: "…Observatory.TransparentProxyExtension")`
and the request failed with: *Unable to find any matched extension with
identifier …TransparentProxyExtension.*

Verified facts at failure time:
- The embedded extension exists at
  `…app/Contents/Library/SystemExtensions/TransparentProxyExtension.systemextension`
  and its `CFBundleIdentifier` is EXACTLY the requested id.
- The extension is codesign-valid and satisfies its DR.
- The app was NOT translocated, NOT quarantined, running from `/Applications`.
- BUT the running GUI process (PID 16127, started 20:47) was an instance of a
  bundle that had been **overwritten in place** by a later `cp -R` after a
  rebuild. The OS resolves embedded system extensions against the *originally
  launched* bundle image; overwriting the bundle under a live process is the
  prime suspect.

**Hypothesized root cause:** stale running process / in-place bundle overwrite,
not a build or identifier defect. **Mitigation already applied:** fully quit the
GUI, `rm -rf` the installed app, fresh `cp -R`, `lsregister -f`, relaunch.

**Open question for review:** are there OTHER common causes of this specific
error we should defend against in code or build config? Candidates to assess:
- Does the app need the extension also under `Contents/PlugIns/` (Xcode warned
  the systemextension is in `Library/SystemExtensions/`, "must be in PlugIns")?
  Is that warning benign for a NESystemExtension, or the actual bug?
- Should `activate()` surface a clearer remediation than the raw error string?

## Problem 2 — onboarding too busy; activation below the fold

Current first-run layout (top→bottom), all in one scroll view:
1. Hero: big headline + paragraph + two buttons (Explore Demo / Set Up Live
   Capture) + 3 trust chips + a sample request-preview card with 3 evidence
   steps.
2. Activation strip: 3 metric tiles.
3. Setup panel (only after clicking "Set Up Live Capture"): a "what live capture
   changes" grid (4 facts) + 4-item checklist + status text + install-command
   box + Copy Install + a row of THREE buttons (Check Status / Enable Live
   Capture / Continue Live) + status text + a Reset row.

Problems: the enable controls are at the very bottom, behind a disclosure, below
multiple dense panels. A first-time user clicks "Set Up Live Capture" and sees
more marketing, not the action. Two parallel button clusters (hero + panel)
confuse "where do I click."

### Proposed redesign — a linear 3-step flow

Replace the hero+strip+disclosure with a single, always-visible **stepper** that
shows exactly one primary action at a time. Demo stays one click away.

```
┌────────────────────────────────────────────┐
│  Agent Observatory                           │
│  See what your agents actually send.         │
│                                              │
│  [ Explore the demo feed ]   ← secondary,    │
│                                 always here   │
│                                              │
│  Turn on live capture (3 steps)              │
│  ① Install the local engine      ✓ done      │
│  ② Enable the capture extension  ● Enable →  │  ← primary CTA, the
│     approve in System Settings when asked       ONE thing to do now
│  ③ Trust the local CA            (auto)      │
│                                              │
│  status line (color-coded)                   │
│  ▸ Trust & privacy  (disclosure, collapsed)  │  ← move the dense
│  ▸ Reset / uninstall (disclosure, collapsed) │     grids behind these
└────────────────────────────────────────────┘
```

Rules:
- The three steps are always visible; each shows ✓ / current / pending. The
  primary button is whichever step is current (Install → Enable → Done).
- "Explore the demo feed" is always available as a secondary action.
- The "what live capture changes" 4-fact grid and the install-command/reset
  boxes move behind collapsed disclosures ("Trust & privacy", "Advanced /
  CLI") so the default view is calm.
- When `proxy.status == .active`: collapse the stepper to a single green
  "Live capture is on" row + a "Go to live feed" primary button.
- Remove the metric strip (fabricated-feeling) and the sample preview card from
  the first screen, OR keep ONE small live demo line. Lean: remove.
- Keep all existing functionality (install cmd copy, reset, check status) but
  demote it; nothing is deleted, just reorganized by altitude.

### Acceptance
- On first run, the "Enable" action is visible without scrolling at 900×600.
- Exactly one primary CTA at a time.
- Demo reachable in one click.
- `proxy.status` drives step ② state and the final "Live is on" collapse.
- App + tests still build; no behavior change to ProxyController/EngineClient.
