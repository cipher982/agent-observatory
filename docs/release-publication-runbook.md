# v0.3 Release Publication Runbook

Use this after the v0.3 readiness gates are green locally:

- `make release`
- `make v03-safe-capture-qa`
- `make v03-installed-daemon-compat-qa`
- `make notarize`
- `make release-qa`
- `make v03-installed-ne-proof`
- live installed NetworkExtension proof is recorded for Claude Code and Codex

Do not publish from a dirty tree or from artifacts that have not passed strict
notarized release QA.

## Required Release Secrets

The manual **macOS Release** GitHub Actions workflow consumes ordinary GitHub
repository secrets:

- `MACOS_SIGNING_CERT_P12_BASE64`
- `MACOS_SIGNING_CERT_PASSWORD`
- `MACOS_PROVISIONING_PROFILE_APP_BASE64`
- `MACOS_PROVISIONING_PROFILE_EXT_BASE64`
- `MACOS_NOTARY_APPLE_ID`
- `MACOS_NOTARY_APP_PASSWORD`
- `MACOS_NOTARY_TEAM_ID`

For David's operator workflow, Infisical is the canonical source and GitHub is
the CI mirror. Keep the public repo contract on normal environment variables and
GitHub secrets; use Infisical only to restore or resync those values.

Check current state at any point:

```bash
make release-secrets-doctor
```

The doctor should pass before running the GitHub **macOS Release** workflow.

Check the Infisical source of truth and resync GitHub from it:

```bash
INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
scripts/release-secrets.sh infisical-doctor

INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
scripts/release-secrets.sh github-from-infisical
```

The default Infisical location is `prod` at `/agent-observatory/release`; set
`INFISICAL_RELEASE_ENV` or `INFISICAL_RELEASE_PATH` only if that changes.

On the release Mac, prefer the staged ceremony script when refreshing everything
from local trust material. It front-loads the human approval prompts, stores the
secrets in Infisical, mirrors them to GitHub Actions, then runs both doctors:

```bash
INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
OP_NOTARY_ITEM=<one-password-item-id-or-name> \
make release-secrets-stage
```

If `MACOS_NOTARY_APPLE_ID`, `MACOS_NOTARY_APP_PASSWORD`, and
`MACOS_NOTARY_TEAM_ID` are already exported, omit `OP_NOTARY_ITEM`.

The provisioning profile secrets must correspond to the manual-signing profile
names used by `app/project.yml`:

- app profile: `Agent Observatory App DevID`
- extension profile: `Agent Observatory Ext DevID`

Useful local profile paths on the release Mac:

```bash
~/Library/Developer/Xcode/UserData/Provisioning\ Profiles/be23edd9-0851-42a3-bd80-4e7d5d1c25a3.provisionprofile
~/Library/Developer/Xcode/UserData/Provisioning\ Profiles/946a428e-5fa9-4671-b7c0-ddd22bf658f3.provisionprofile
```

Set the two profile secrets from the release Mac:

```bash
scripts/release-secrets.sh set-profiles
```

Store the same profiles in Infisical:

```bash
INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
scripts/release-secrets.sh set-infisical-profiles
```

Export the Developer ID certificate as a password-protected `.p12` using Keychain
Access or `security export`, then set:

```bash
scripts/release-secrets.sh set-signing-cert /path/to/developer-id-application.p12
```

Store the same Developer ID certificate in Infisical:

```bash
INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
scripts/release-secrets.sh set-infisical-signing-cert /path/to/developer-id-application.p12
```

Set notary credentials with the Apple-ID app-specific-password convention:

```bash
MACOS_NOTARY_APPLE_ID=<apple-id> \
MACOS_NOTARY_APP_PASSWORD=<app-specific-password> \
MACOS_NOTARY_TEAM_ID=M49WM6JSW8 \
scripts/release-secrets.sh set-notary-from-env
```

Store the same notary credentials in Infisical:

```bash
INFISICAL_RELEASE_PROJECT_ID=<infisical-project-id> \
MACOS_NOTARY_APPLE_ID=<apple-id> \
MACOS_NOTARY_APP_PASSWORD=<app-specific-password> \
MACOS_NOTARY_TEAM_ID=M49WM6JSW8 \
scripts/release-secrets.sh set-infisical-notary-from-env
```

`MACOS_NOTARY_TEAM_ID` may also be set independently to `M49WM6JSW8`; it is not
secret, but keeping it in the same secret lane simplifies the workflow.

## Local Release Path

```bash
git status --short --branch
make release
make v03-safe-capture-qa
MACOS_NOTARY_APPLE_ID=<apple-id> \
MACOS_NOTARY_APP_PASSWORD=<app-specific-password> \
MACOS_NOTARY_TEAM_ID=<team-id> \
make notarize
make release-qa
cat dist/SHA256SUMS
```

Equivalent supported auth modes:

```bash
NOTARY_PROFILE=<notarytool-keychain-profile> make notarize
```

```bash
APP_STORE_CONNECT_KEY_ID=<key-id> \
APP_STORE_CONNECT_API_KEY_P8=/path/to/AuthKey_<key-id>.p8 \
APP_STORE_CONNECT_ISSUER_ID=<issuer-uuid> \
make notarize
```

## CI Release Path

After all release secrets are configured, run the manual workflow:

```bash
gh workflow run "macOS Release" \
  --repo cipher982/agent-observatory \
  -f version=0.3.0 \
  -F stage_release=false
```

When that passes, rerun with draft staging:

```bash
gh workflow run "macOS Release" \
  --repo cipher982/agent-observatory \
  -f version=0.3.0 \
  -F stage_release=true
```

The workflow runs `make release`, `make v03-safe-capture-qa`,
`make v03-installed-daemon-compat-qa`, `make notarize`, and `make release-qa`,
then uploads the notarized artifacts and optionally creates or updates the
matching draft GitHub release.

## Install And Live Proof

After notarization, replace the installed app with the stapled 0.3 artifact:

```bash
rm -rf "/Applications/Agent Observatory.app"
cp -R "dist/Agent Observatory.app" /Applications/
"/Applications/Agent Observatory.app/Contents/Resources/agents" install
make v03-installed-daemon-compat-qa
open "/Applications/Agent Observatory.app"
```

Then enable live capture from the app, approve the System Extension if prompted,
and record:

```bash
make v03-installed-ne-proof
"/Applications/Agent Observatory.app/Contents/Resources/agents" doctor wire
```

Final live proof must include:

- Claude Code request completes and appears as a full capture.
- Codex request completes and appears as a full capture.
- `make v03-installed-ne-proof` shows an unsupported provider-bound request
  completes, produces no body capture, and appears as a pass-through coverage
  event.

## Publish

Do not publish until `docs/v0.3-launch-readiness.md` records exact commands,
dates, and observed outputs for the public-GO gates. The draft release should
include:

```bash
dist/Agent-Observatory-0.3.0-macOS.dmg
dist/Agent-Observatory-0.3.0-macOS.zip
dist/agents
dist/SHA256SUMS
```
