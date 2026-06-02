# v0.2 Release Publication Runbook

Use this after the v0.2 readiness gates are green locally:

- `make release`
- `NOTARY_PROFILE=<profile> make notarize`
  - or `APP_STORE_CONNECT_KEY_ID=... APP_STORE_CONNECT_API_KEY_P8=... make notarize`
- `make release-qa`
- `make v02-readiness` passes except for the remote-release gate
- real Claude Code live NE proof is recorded

Do not publish from a dirty tree or from artifacts that have not passed strict
notarized release QA.

## 1. Confirm Local Commit And Artifacts

```bash
git status --short --branch
git rev-parse HEAD
cat dist/SHA256SUMS
make release-qa
```

Expected:

- no uncommitted release changes;
- `make release-qa` passes;
- `dist/SHA256SUMS` matches the final stapled artifacts.

## 2. Push The Release Commit

Push the branch that contains the final release commit:

```bash
git push origin ne-epic
```

If `main` is the public badge/release branch, merge or fast-forward `main`
according to the repo's normal policy, then confirm CI:

```bash
gh run list --repo cipher982/agent-observatory --limit 5
```

## 3. Publish Strategy

Create a new `v0.2.0` release from the final commit. This avoids mutating the
already-published stale `v0.1.0` release and makes the fixed capture pipeline
milestone clear. `make v02-readiness` requires a `v0.2.0` release whose tag and
asset digests match the local final artifacts.

For pre-publication staging, create the release as a **draft** with the same
assets. GitHub draft releases may show asset URLs under an `untagged-*` slug
until publication; that is normal. The readiness audit accepts a draft only when
its target commit and asset digests match the local final state.

If the app build number changes after a draft is staged, treat the draft assets
as stale. Rebuild, notarize, delete/replace all draft assets, and retarget the
draft before publishing.

## 4. Create v0.2.0

```bash
gh release create v0.2.0 \
  --repo cipher982/agent-observatory \
  --draft \
  --latest=false \
  --target "$(git rev-parse HEAD)" \
  --title "Agent Observatory v0.2.0" \
  --notes-file docs/release-v0.2-draft.md \
  dist/Agent-Observatory-0.2.0-macOS.dmg \
  dist/Agent-Observatory-0.2.0-macOS.zip \
  dist/agents \
  dist/SHA256SUMS
```

Do not create the release until the `VERSION`, app `MARKETING_VERSION`, and
bundled helper version all report `0.2.0`.

Equivalent guarded helper:

```bash
CONFIRM_PUBLISH_V02=1 scripts/v02-finalize.sh --publish
```

For an existing draft, the guarded helper uploads the current local artifacts
with `--clobber` before publishing.

When the final live proof is recorded, retarget the draft to the final commit if
needed, refresh the notes, then publish:

```bash
gh release edit v0.2.0 \
  --repo cipher982/agent-observatory \
  --target "$(git rev-parse HEAD)" \
  --notes-file docs/release-v0.2-draft.md \
  --draft=false
```

## 5. Verify Publication

```bash
gh release view v0.2.0 \
  --repo cipher982/agent-observatory \
  --json tagName,name,targetCommitish,assets,url

make v02-readiness
```

Expected:

- release title uses **Agent Observatory**;
- tag points at the final commit;
- asset digests match `dist/SHA256SUMS`;
- `make v02-readiness` remote-release gate passes.
