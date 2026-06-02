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
run_stage=0
run_publish=0

usage() {
  cat <<'USAGE'
usage: scripts/v02-finalize.sh [--trust] [--notarize] [--stage] [--publish]

Default mode is read-only and prints the current final-gate state.

Flags:
  --trust     Run agents trust install. This intentionally triggers a macOS
              Security authorization prompt for login-keychain CA trust.
  --notarize  Run make notarize, then make release-qa.
              Requires NOTARY_PROFILE, or APP_STORE_CONNECT_KEY_ID plus
              APP_STORE_CONNECT_API_KEY_P8.
  --stage     Upload current dist assets to the v0.2.0 draft release and retarget
              it to HEAD, while keeping the release unpublished. Requires
              CONFIRM_STAGE_V02=1 and strict release QA to pass.
  --publish   Create GitHub release v0.2.0 from current HEAD and dist assets.
              Requires CONFIRM_PUBLISH_V02=1 and strict release QA to pass.
              Refuses to mutate an already-public release unless
              CONFIRM_REPUBLISH_V02=1 is also set.

Run order for a real release:
  1. reset Observatory capture config from the fixed app if old builds are waiting
  2. scripts/v02-finalize.sh --trust
  3. NOTARY_PROFILE=<profile> scripts/v02-finalize.sh --notarize
     # or APP_STORE_CONNECT_KEY_ID=... APP_STORE_CONNECT_API_KEY_P8=... scripts/v02-finalize.sh --notarize
  4. approve/enable the NetworkExtension from the app and prove Claude Code live
  5. CONFIRM_STAGE_V02=1 scripts/v02-finalize.sh --stage
  6. CONFIRM_PUBLISH_V02=1 scripts/v02-finalize.sh --publish
  7. make v02-readiness
USAGE
}

for arg in "$@"; do
  case "$arg" in
    --trust) run_trust=1 ;;
    --notarize) run_notarize=1 ;;
    --stage) run_stage=1 ;;
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

require_clean_release_tree() {
  tracked_dirty="$(git status --porcelain --untracked-files=no)"
  untracked_release="$(git ls-files --others --exclude-standard app backend scripts docs Makefile README.md SECURITY.md 2>/dev/null || true)"
  if [ -n "$tracked_dirty" ] || [ -n "$untracked_release" ]; then
    echo "v02-finalize: release tree has uncommitted changes; commit or clean release-affecting files before publishing" >&2
    if [ -n "$tracked_dirty" ]; then
      sed 's/^/  /' <<<"$tracked_dirty" >&2
    fi
    if [ -n "$untracked_release" ]; then
      sed 's/^/  ?? /' <<<"$untracked_release" >&2
    fi
    exit 2
  fi
}

expected_build_version() {
  sed -n 's/^[[:space:]]*CURRENT_PROJECT_VERSION:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' app/project.yml | head -n1
}

require_dist_build_current() {
  expected="$(expected_build_version)"
  app_build="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "dist/$APP_NAME/Contents/Info.plist" 2>/dev/null || true)"
  ext_build="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "dist/$APP_NAME/Contents/Library/SystemExtensions/com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension.systemextension/Contents/Info.plist" 2>/dev/null || true)"
  if [ -z "$expected" ] || [ "$app_build" != "$expected" ] || [ "$ext_build" != "$expected" ]; then
    echo "v02-finalize: dist build does not match project build (project=${expected:-unknown}, app=${app_build:-unknown}, ext=${ext_build:-unknown})" >&2
    exit 1
  fi
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

if [ "$run_trust" -eq 0 ] && [ "$run_notarize" -eq 0 ] && [ "$run_stage" -eq 0 ] && [ "$run_publish" -eq 0 ]; then
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
  if [ -z "${NOTARY_PROFILE:-}" ] &&
     { [ -z "${APP_STORE_CONNECT_KEY_ID:-}" ] || [ -z "${APP_STORE_CONNECT_API_KEY_P8:-}" ]; }; then
    echo "v02-finalize: set NOTARY_PROFILE=<notarytool profile> or APP_STORE_CONNECT_KEY_ID + APP_STORE_CONNECT_API_KEY_P8" >&2
    exit 2
  fi
  NOTARY_PROFILE="${NOTARY_PROFILE:-}" make notarize
  make release-qa
fi

if [ "$run_stage" -eq 1 ]; then
  section "Stage v0.2.0 Draft"
  test "${CONFIRM_STAGE_V02:-}" = "1" || {
    echo "v02-finalize: set CONFIRM_STAGE_V02=1 to mutate the GitHub draft release v0.2.0" >&2
    exit 2
  }
  require_path "$DMG"
  require_path "$ZIP"
  require_path dist/agents
  require_path dist/SHA256SUMS
  require_clean_release_tree
  require_dist_build_current
  make release-qa
  if gh release view v0.2.0 --repo cipher982/agent-observatory >/dev/null 2>&1; then
    is_draft="$(gh release view v0.2.0 --repo cipher982/agent-observatory --json isDraft --jq .isDraft)"
    if [ "$is_draft" != "true" ]; then
      echo "v02-finalize: v0.2.0 is already public; refusing to restage it" >&2
      exit 2
    fi
    gh release upload v0.2.0 \
      --repo cipher982/agent-observatory \
      --clobber \
      "$DMG" \
      "$ZIP" \
      dist/agents \
      dist/SHA256SUMS
    gh release edit v0.2.0 \
      --repo cipher982/agent-observatory \
      --target "$(git rev-parse HEAD)" \
      --title "Agent Observatory v0.2.0" \
      --notes-file docs/release-v0.2-draft.md \
      --draft
  else
    gh release create v0.2.0 \
      --repo cipher982/agent-observatory \
      --target "$(git rev-parse HEAD)" \
      --title "Agent Observatory v0.2.0" \
      --notes-file docs/release-v0.2-draft.md \
      --draft \
      "$DMG" \
      "$ZIP" \
      dist/agents \
      dist/SHA256SUMS
  fi
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
  require_clean_release_tree
  require_dist_build_current
  make release-qa
  if gh release view v0.2.0 --repo cipher982/agent-observatory >/dev/null 2>&1; then
    is_draft="$(gh release view v0.2.0 --repo cipher982/agent-observatory --json isDraft --jq .isDraft)"
    if [ "$is_draft" != "true" ] && [ "${CONFIRM_REPUBLISH_V02:-}" != "1" ]; then
      echo "v02-finalize: v0.2.0 is already public; set CONFIRM_REPUBLISH_V02=1 to mutate published assets" >&2
      exit 2
    fi
    gh release upload v0.2.0 \
      --repo cipher982/agent-observatory \
      --clobber \
      "$DMG" \
      "$ZIP" \
      dist/agents \
      dist/SHA256SUMS
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
