# Agent Context Observatory Public Launch Epic

Status: active
Owner: product + engineering
Target: public source launch, then Hacker News-ready release candidate

## Executive Summary

Agent Context Observatory should become the default local debugger for coding
agent context. A developer installs it once, keeps using their normal agents, and
can see what actually reached the model: instructions, skills, tools, transcript
evidence, and verified outbound request facts.

The launch bar is not "prototype works on one machine." The launch bar is that a
skeptical developer can read the repo, understand the trust model, build or run
the app, see a polished demo immediately, and believe the install-once capture
path is technically sound.

## Product Promise

After one command, newly launched agents are captured automatically. There is no
hosted account, no browser extension, no wrapper-first workflow, and no managed
launch requirement. Demo mode exists only so first-run comprehension is instant;
the product's real path is normal agent usage after install.

## Current State

Already complete:
- Public GitHub repository exists.
- macOS app builds and launches.
- Go backend exposes CLI, localhost API, SSE stream, transcript discovery,
  context resolution, fact merging, proxy capture, and install lifecycle.
- Demo mode shows synthetic live wire captures.
- Install/status/uninstall are implemented and covered by a fake-home loop.
- Custom Dock/menu bar identity exists.
- Local release artifacts can be produced with checksums.

Known gaps:
- Fresh public screenshots and a short demo clip are missing.
- GitHub CI is blocked until the publishing token has `workflow` scope.
- Public binary distribution still needs Developer ID signing and notarization.
- First-run/live/degraded states need a final product-language polish pass.
- Verified capture needs a documented compatibility matrix across supported
  provider shapes and agent runtimes.

## Success Criteria

- README has a launch-quality story: problem, product, demo, install, security,
  limitations, roadmap, and fresh visuals.
- A fresh clone can run backend QA and build the macOS app using documented
  commands.
- Demo mode is deterministic and visually strong enough for screenshots.
- Live mode clearly separates connected, reconnecting, degraded, and uninstalled
  states without implementation jargon.
- `agents install`, `agents status`, and `agents uninstall` are idempotent,
  reversible, and understandable.
- Verified capture is tested for OpenAI Responses/chat, Anthropic Messages, and
  Bedrock Anthropic request bodies.
- The repo contains no private screenshots, local captures, generated binaries,
  or personal workstation assumptions.
- A release candidate has a zip, CLI binary, SHA256 sums, launch note, and
  documented notarization status.
- Any external blocker is explicit and narrow.

## Non-Goals

- Hosted/team observability.
- Enterprise policy management.
- App Store distribution.
- Making Observatory the launcher for agents.
- Passive HTTPS body inspection without explicit proxy/trust setup.
- Canonical context upstreaming. This remains the next product frontier after
  observability proves demand.

## Decision Log

### Decision: Source-First Public Launch

Context: The current app is useful and public-source-ready, but binary
distribution still needs notarization.

Choice: Launch the repo source-first while preparing a signed/notarized binary
release as a separate gate.

Rationale: The product category is trust-sensitive. Public source credibility is
valuable even before a frictionless binary download exists.

Revisit if: Developer ID credentials become available and notarization passes.

### Decision: Demo Mode Is A Product Surface

Context: First-time users may not have agent traffic or proxy trust configured.

Choice: Keep demo mode prominent and polished.

Rationale: The product needs to show the value before asking a user to alter
their agent process environment.

Revisit if: Real capture can become self-verifying on first launch.

### Decision: Red Dot Means Degraded Only

Context: Menu bar icons have limited information bandwidth.

Choice: Use the custom observatory dome as the base mark and add a red dot only
for degraded/reconnecting states.

Rationale: Multiple icon modes made the mark feel busy. A single degraded
indicator is clearer.

Revisit if: Users need a richer menu bar status surface after launch.

### Decision: No Managed Launch Requirement

Context: The core wedge against heavier tools is friction.

Choice: Primary capture path is install once, then use existing agents normally.

Rationale: If users must remember wrapper commands, the product loses its
advantage over existing observability stacks.

Revisit if: Some runtime cannot inherit proxy/trust environment reliably.

## Phases

### Phase 0: Public Source Baseline

Status: complete

Acceptance:
- Repo is public and pushed.
- Private screenshots are removed.
- README and release docs describe the source-first release state.
- Backend QA, app build, and release build have passed locally.

Verification:
- Public repo: `cipher982/agent-observatory`
- Last verified commands: `make backend-qa`, `make app-build`, `make release`

### Phase 1: Launch Spec And Repo Hygiene

Status: complete

Acceptance:
- This epic exists and is committed.
- The old week-one plan is either linked or superseded by this epic.
- Local ignored artifacts do not affect `git status`.
- The repo has a short public roadmap section in README.

Verification:
- `git status --short`
- `git diff --check`
- Committed in `9cff9c0`.

### Phase 2: First-Run And Demo Polish

Status: complete

Acceptance:
- Demo/live labels use product language, not implementation language.
- Empty state explains the next action without wrapper-first instructions.
- Menu bar status is readable at a glance.
- UI text fits in common window sizes.
- App screenshot review passes on a clean demo feed.

Verification:
- `make app-build`
- Launch app in demo mode and capture a fresh screenshot.
- Demo mode serves sanitized sample sessions instead of reading local transcripts.

### Phase 3: Public Launch Assets

Status: complete

Acceptance:
- `docs/screenshots/live-feed.png` is generated from sanitized demo data.
- README includes the screenshot.
- `docs/launch-note.md` is concise enough to post externally.
- Launch assets contain no local desktop context.

Verification:
- Window-only screenshot capture.
- Public hygiene scan over docs.
- README references `docs/screenshots/live-feed.png`.

### Phase 4: Capture And Install Hardening

Status: complete

Acceptance:
- Provider parser tests cover OpenAI Responses/chat, Anthropic Messages, and
  Bedrock Anthropic bodies.
- Install lifecycle QA still passes.
- `agents status` clearly reports installed, partially installed, and missing
  states.
- The trust model is documented in CLI and README language.

Verification:
- `make backend-qa`
- Manual `agents status` smoke check against a fake or local target.
- `agents status` distinguishes installed, partially installed, and absent states.
- `agents doctor wire` directly names the local MITM hop and upstream TLS boundary.

### Phase 5: Distribution Readiness

Status: pending

Acceptance:
- `make release` creates zip, CLI binary, and checksums.
- Release docs clearly distinguish ad-hoc local builds from notarized public
  binaries.
- If signing credentials are unavailable, notarization is recorded as the only
  release blocker.
- GitHub CI is added once token scope allows workflow creation.

Verification:
- `make release`
- `gh repo view`
- GitHub workflow push if token scope is updated.

### Phase 6: Final Launch Review

Status: pending

Acceptance:
- Independent agent review approves README, security model, and launch assets.
- Public repo hygiene scan has no private-data findings.
- Fresh clone instructions are plausible and complete.
- All completed work is committed and pushed.

Verification:
- Hatch review.
- Public hygiene scan.
- `git status --short`
- `git push`

## Operating Plan

Work one phase at a time. Commit every meaningful completed phase or fix. Do not
hide external blockers: token scope, Developer ID signing, and runtime-specific
environment inheritance must be called out plainly when they block release
quality.
