#!/usr/bin/env bash
# Guarded helper for the last v0.2 release gates.
#
# Default is read-only status. Opt-in flags run steps that may prompt macOS or
# publish external release state.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APP_NAME="Agent Observatory.app"
HELPER="/Applications/$APP_NAME/Contents/Resources/agents"
DMG="dist/Agent-Observatory-0.2.0-macOS.dmg"
ZIP="dist/Agent-Observatory-0.2.0-macOS.zip"

run_trust=0
run_notarize=0
run_publish=0

usage() {
  cat <<'USAGE'
usage: scripts/v02-finalize.sh [--trust] [--notarize] [--publish]

Default mode is read-only and prints the current final-gate state.

Flags:
  --trust     Run agents trust install. This intentionally triggers a macOS
              Security authorization prompt for login-keychain CA trust.
  --notarize  Run NOTARY_PROFILE=<profile> make notarize, then make release-qa.
              Requires NOTARY_PROFILE in the environment.
  --publish   Create GitHub release v0.2.0 from current HEAD and dist assets.
              Requires CONFIRM_PUBLISH_V02=1 and strict release QA to pass.

Run order for a real release:
  1. reboot if systemextensionsctl shows old versions waiting to uninstall
  2. scripts/v02-finalize.sh --trust
  3. NOTARY_PROFILE=<profile> scripts/v02-finalize.sh --notarize
  4. approve/enable the NetworkExtension from the app and prove Claude Code live
  5. CONFIRM_PUBLISH_V02=1 scripts/v02-finalize.sh --publish
  6. make v02-readiness
USAGE
}

for arg in "$@"; do
  case "$arg" in
    --trust) run_trust=1 ;;
    --notarize) run_notarize=1 ;;
    --publish) run_publish=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "v02-finalize: unknown argument $arg" >&2; usage >&2; exit 2 ;;
  esac
done

section() {
  printf "\n== %s ==\n" "$1"
}

require_path() {
  test -e "$1" || { echo "v02-finalize: missing $1" >&2; exit 1; }
}

status_snapshot() {
  section "Status"
  git status --short --branch
  git rev-parse HEAD

  section "Installed App"
  if [ -x "$HELPER" ]; then
    "$HELPER" version || true
    "$HELPER" status || true
    "$HELPER" trust status || true
  else
    echo "missing installed helper: $HELPER"
  fi

  section "Daemon"
  curl -fsS http://127.0.0.1:7878/healthz || true

  section "System Extensions"
  systemextensionsctl list 2>/dev/null | grep agentobservatory || true

  section "Release"
  gh release view v0.2.0 \
    --repo cipher982/agent-observatory \
    --json tagName,name,targetCommitish,assets,url 2>/dev/null || echo "v0.2.0 release not found"
}

if [ "$run_trust" -eq 0 ] && [ "$run_notarize" -eq 0 ] && [ "$run_publish" -eq 0 ]; then
  status_snapshot
  exit 0
fi

require_path "$HELPER"

if [ "$run_trust" -eq 1 ]; then
  section "Trust Login Keychain CA"
  echo "This step may show a macOS Security authorization prompt."
  "$HELPER" trust install
fi

if [ "$run_notarize" -eq 1 ]; then
  section "Notarize"
  test -n "${NOTARY_PROFILE:-}" || {
    echo "v02-finalize: set NOTARY_PROFILE=<notarytool keychain profile>" >&2
    exit 2
  }
  NOTARY_PROFILE="$NOTARY_PROFILE" make notarize
  make release-qa
fi

if [ "$run_publish" -eq 1 ]; then
  section "Publish v0.2.0"
  test "${CONFIRM_PUBLISH_V02:-}" = "1" || {
    echo "v02-finalize: set CONFIRM_PUBLISH_V02=1 to publish GitHub release v0.2.0" >&2
    exit 2
  }
  require_path "$DMG"
  require_path "$ZIP"
  require_path dist/agents
  require_path dist/SHA256SUMS
  make release-qa
  if gh release view v0.2.0 --repo cipher982/agent-observatory >/dev/null 2>&1; then
    gh release edit v0.2.0 \
      --repo cipher982/agent-observatory \
      --target "$(git rev-parse HEAD)" \
      --title "Agent Observatory v0.2.0" \
      --notes-file docs/release-v0.2-draft.md \
      --draft=false
  else
    gh release create v0.2.0 \
      --repo cipher982/agent-observatory \
      --target "$(git rev-parse HEAD)" \
      --title "Agent Observatory v0.2.0" \
      --notes-file docs/release-v0.2-draft.md \
      "$DMG" \
      "$ZIP" \
      dist/agents \
      dist/SHA256SUMS
  fi
fi

status_snapshot
