# Agent Observatory Agent Guide

## Start Here

Agent Observatory is a local-first macOS app plus Go backend/CLI for observing
agent instructions, transcript facts, and outbound LLM request evidence.

For a quick repo shape:

```bash
git ls-files | sed -n '1,160p'
```

Core checks:

```bash
make backend-qa
make v03-safe-capture-qa
make v03-installed-daemon-compat-qa
```

Full release proof is CI-backed. Do not publish a release without explicit user
approval.

## Release Secrets

If you are touching macOS signing, notarization, GitHub release secrets, or the
`macOS Release` workflow, start here:

```bash
make release-secrets-doctor
```

If secrets or local release auth are incomplete, use the front-loaded ceremony:

```bash
INFISICAL_RELEASE_PROJECT_ID=<project-id> \
OP_NOTARY_ITEM=<one-password-item-id-or-name> \
make release-secrets-stage
```

If `MACOS_NOTARY_APPLE_ID`, `MACOS_NOTARY_APP_PASSWORD`, and
`MACOS_NOTARY_TEAM_ID` are already exported, omit `OP_NOTARY_ITEM`.

Do not probe 1Password, Keychain, and GitHub secrets piecemeal. The staged
workflow is intentionally designed so human approval prompts happen up front,
then Infisical and GitHub sync run non-interactively. Infisical is David's
operator source of truth; the public repo and GitHub Actions consume ordinary
environment variables/secrets.

Reference: `docs/release-publication-runbook.md`.

## Build And Test

Useful commands:

```bash
make qa
make release
make notarize
make release-qa
```

Backend-only:

```bash
make -C backend qa
go test ./cmd/agents ./internal/projectindex
```

App project generation/build:

```bash
make app-project
make app-build
```

## Architecture

- `backend/`: Go engine, API, proxy, CLI, transcript/fact logic.
- `app/`: SwiftUI macOS app and NetworkExtension project.
- `scripts/`: release, notarization, QA, and v0.3 proof scripts.
- `docs/`: launch specs, release runbooks, architecture notes, screenshots.
- `.github/workflows/release-macos.yml`: source-built macOS release workflow.

The backend is the source of truth. The app, CLI, and API render the same
fact-level model.

## Boundaries

- Never commit secrets, exported certificates, provisioning profiles, or local
  release artifacts.
- Never use `--no-verify` or rewrite published history.
- Ask before publishing a GitHub release or changing version numbers.
- Keep public/OSS docs secret-manager-agnostic; Infisical belongs to David's
  operator workflow, not the user-facing install path.
