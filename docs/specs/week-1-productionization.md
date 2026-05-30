# Week 1 Productionization Plan

Goal: make Agent Context Observatory credible as a public, local-first developer
tool that can survive a Hacker News launch and convert curious builders into
successful first-run users.

## Release Success Criteria

- Public GitHub repository is clean: no private screenshots, local captures,
  personal workstation assumptions, generated build artifacts, or secrets.
- A new developer can clone the repo, run backend CI locally, and build the macOS
  app from documented commands.
- A macOS user can open the app, understand the product in demo mode, install
  ambient capture with one command, verify status, and uninstall cleanly.
- The security model is obvious: local-only engine, no hosted account, derived
  fact persistence, explicit local proxy for verified capture, normal provider
  TLS upstream.
- A release candidate exists with checksums, a launch note, and fresh sanitized
  screenshots or a short demo clip.

## Goals

### 1. Public Repo Foundation

Acceptance:
- GitHub repo is public and pushed from local `main`.
- README is accurate for an outside user.
- CI runs backend build, vet, and short tests.
- Old private screenshots stay out of the public history from this point forward.

### 2. First-Run Product Polish

Acceptance:
- Demo mode is visually strong and deterministic.
- Empty/live states avoid implementation jargon.
- Menu bar and Dock icon use the custom observatory dome identity.
- Status indicators are simple: normal, degraded red dot, disconnected.

### 3. Install-Once Capture Hardening

Acceptance:
- `agents install`, `agents status`, and `agents uninstall` stay idempotent under
  repeated QA loops.
- Status explains exactly what is installed and what the user should do next.
- No primary workflow depends on wrapper commands or managed launches.
- Verified capture path is tested for OpenAI, Anthropic, and Bedrock-shaped
  request bodies.

### 4. Release Distribution

Acceptance:
- `make release` creates a zipped app, CLI binary, and SHA256 sums.
- Release docs distinguish local ad-hoc builds from public notarized downloads.
- If Developer ID credentials are available, produce a signed/notarized release;
  otherwise leave a clear release blocker and ship source-first.

### 5. Launch Assets

Acceptance:
- New screenshots are generated from sanitized demo data only.
- Launch note is tight enough to post directly.
- README has a concise roadmap that frames observability now and canonical
  context management later.

## This Week's Done State

By the end of the week, the project should have a public repo, passing backend
CI, clean docs, a reproducible local release artifact, fresh launch-safe visuals,
and one verified fresh-machine install/uninstall pass recorded in the release
notes.
