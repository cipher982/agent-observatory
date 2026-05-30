# Agent Context Observatory Release Readiness

Status: release-ready
Target: Hacker News-ready local-first macOS developer app

## Executive Summary

Agent Context Observatory answers one product question: when a coding agent runs,
what instructions, skills, tools, and context did it actually receive?

The release target is a polished local app that a technical user can install,
understand in under two minutes, and use to see real or demo agent traffic without
reading implementation notes. The backend remains the source of truth; the native
app is the primary experience; docs and release artifacts make it credible enough
for public scrutiny.

## Success Criteria

- A fresh checkout can build the backend and macOS app from documented commands.
- The app launches without manual setup, starts its bundled engine, and shows a
  useful empty/demo state instead of raw implementation guidance.
- Demo mode produces a visually compelling, deterministic live feed suitable for
  screenshots and a short launch video.
- Real mode can show current local agent sessions and live proxy captures.
- Install, status, and uninstall flows are safe, reversible, and explained in
  product language with the security model stated plainly.
- Backend QA passes: build, vet, unit tests, race tests, and install lifecycle.
- App QA passes: project generation, Debug build, launched app smoke test, and
  screenshot verification.
- The repository has a clean source boundary: no generated binaries, no local
  captures, no Xcode derived state, and no private personal-machine assumptions
  required for basic demo/build.
- README is launch-ready: problem, product, screenshots, install/build, demo,
  security model, limitations, and roadmap.
- Release artifacts exist: app bundle or zipped app, CLI binary, checksums, and
  a concise launch note.

## Non-Goals For This Release

- App Store distribution.
- Multi-user/team cloud observability.
- Centralized hosted storage.
- Full canonical context control/upstreaming. This release observes first.
- Passive HTTPS body capture without an explicit proxy/trust setup.

## Product Decisions

### Decision: Local-First V1

Context: The product inspects agent prompts and tool schemas, which can be
sensitive.

Choice: Ship as a local-only app with a localhost engine and local capture state.

Rationale: Hacker News users will trust a transparent local tool faster than an
opaque hosted service for this category.

Revisit if: Team workflows or remote fleet observability become the primary use
case.

### Decision: Demo Mode Is First-Class

Context: A user may not have Claude, Codex, or proxy trust configured when first
opening the app.

Choice: Make demo mode a deliberate, beautiful path that shows the core value
without requiring live agent traffic.

Rationale: The launch needs immediate comprehension. Real capture can remain a
second step.

Revisit if: Real capture becomes automatic enough that demo mode is unnecessary.

### Decision: Native App Is The Launch Surface

Context: The backend has CLI and API surfaces, but the product needs a memorable
first impression.

Choice: Lead with the macOS app, while keeping CLI/API documented for power users.

Rationale: The native live feed is the clearest product expression.

Revisit if: Distribution friction on macOS 26 blocks broad testing.

## Architecture

- Backend: Go engine exposing CLI commands, localhost JSON API, SSE stream,
  transcript discovery, fact/evidence model, wire capture, and install lifecycle.
- App: SwiftUI macOS app that spawns the bundled Go engine, renders session facts,
  shows realtime wire activity, and provides onboarding/demo affordances.
- Docs/release: README plus scripted build/QA/release commands.

## Phases

### Phase 0: Repo And Spec

Status: complete

Acceptance criteria:
- Git repository initialized.
- Generated app/backend binaries ignored.
- This spec committed as the release source of truth.

### Phase 1: Baseline QA

Status: complete

Acceptance criteria:
- Backend `make qa` passes.
- Xcode project generation works.
- macOS app Debug build succeeds from CLI.
- Known failures are documented as release blockers, not left implicit.

Verification:
- `cd backend && make qa` passed.
- `cd app && xcodegen generate` passed.
- `xcodebuild -project Observatory.xcodeproj -scheme Observatory -configuration Debug -derivedDataPath /tmp/observatory-dd build` passed.

### Phase 2: Product Onboarding And Demo

Status: complete

Acceptance criteria:
- App has a polished first-run/empty state that explains the value in user terms.
- Demo mode is available without environment-variable knowledge.
- Demo feed is deterministic and visually strong enough for screenshots.
- README points users to demo mode before advanced proxy setup.

Verification:
- The app defaults to Demo mode with an in-app Demo/Live segmented control.
- Packaged app smoke test showed the demo live feed rendering wire cards.
- README now leads with the product problem and demo-first quick start.

### Phase 3: Packaging And Release Commands

Status: complete

Acceptance criteria:
- One command builds backend helper and app bundle.
- One command creates release artifacts and checksums.
- Release artifacts exclude local state and private data.
- App version is consistent across backend, project config, and Info.plist.

Verification:
- `make release` created the app zip, CLI binary, and `SHA256SUMS` in `dist/`.
- CLI reports `agents-observatory 0.1.0`.
- Built app bundle reports `CFBundleShortVersionString = 0.1.0`.

### Phase 4: Security And Trust

Status: complete

Acceptance criteria:
- README clearly explains what is captured, what is never persisted, how proxy
  trust works, and how uninstall restores state.
- CLI status explains install state and next action in plain language.
- Install/uninstall QA remains green.

Verification:
- README includes the local-first security model and raw-prompt persistence boundary.
- Install/status/uninstall CLI output is plain-language and reversible.
- `make release` reran backend install lifecycle QA successfully.

### Phase 5: Final Release QA

Status: complete

Acceptance criteria:
- Backend QA, app build, launch smoke test, and screenshot review all pass.
- README and launch note are accurate against the built artifact.
- Final git status contains only intentional source/release changes.

Verification:
- `make release` passed.
- Packaged app launched from `dist/Observatory.app` and rendered the demo feed.
- Release artifacts:
  - `dist/Agent-Context-Observatory-0.1.0-macos.zip`
  - `dist/agents`
  - `dist/SHA256SUMS`

## Verification Commands

```bash
cd backend && make qa
cd app && xcodegen generate
xcodebuild -project app/Observatory.xcodeproj -scheme Observatory -configuration Debug -derivedDataPath /tmp/observatory-dd build
```
