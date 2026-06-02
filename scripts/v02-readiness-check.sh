#!/usr/bin/env bash
# Read-only v0.2 readiness audit.
#
# This script is intentionally non-mutating: it does not install, notarize,
# activate NetworkExtension, publish releases, open Finder, or call `make`.
set -u -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.2.0}"
APP_NAME="Agent Observatory.app"
DMG_NAME="Agent-Observatory-${VERSION}-macOS.dmg"
ZIP_NAME="Agent-Observatory-${VERSION}-macOS.zip"
DIST="$ROOT/dist"
APP_PATH="$DIST/$APP_NAME"
INSTALLED_APP_PATH="/Applications/$APP_NAME"
DMG_PATH="$DIST/$DMG_NAME"
ZIP_PATH="$DIST/$ZIP_NAME"
HELPER_PATH="$APP_PATH/Contents/Resources/agents"
INSTALLED_HELPER="$INSTALLED_APP_PATH/Contents/Resources/agents"
EXPECTED_BUILD_VERSION="$(sed -n 's/^[[:space:]]*CURRENT_PROJECT_VERSION:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' app/project.yml | head -n1)"

pass=0
warn=0
fail=0

section() {
  printf "\n== %s ==\n" "$1"
}

ok() {
  pass=$((pass + 1))
  printf "PASS  %s\n" "$1"
}

note() {
  warn=$((warn + 1))
  printf "WARN  %s\n" "$1"
}

bad() {
  fail=$((fail + 1))
  printf "FAIL  %s\n" "$1"
}

have() {
  command -v "$1" >/dev/null 2>&1
}

section "Docs"
for f in README.md SECURITY.md docs/v0.2-readiness.md docs/ne-reset-runbook.md docs/launch-readiness.md; do
  if [ -f "$f" ]; then ok "$f exists"; else bad "$f missing"; fi
done

if grep -q -- '--skip-jenkins' scripts/make-dmg.sh && grep -q 'DMG_STYLE.*headless' Makefile; then
  ok "default release path is headless"
else
  bad "default release path is not clearly headless"
fi

section "Git State"
tracked_dirty="$(git status --porcelain --untracked-files=no 2>/dev/null || true)"
untracked_release="$(git ls-files --others --exclude-standard app backend scripts docs Makefile README.md SECURITY.md 2>/dev/null || true)"
if [ -z "$tracked_dirty" ]; then
  ok "tracked release tree has no uncommitted changes"
else
  bad "tracked release tree has uncommitted changes"
  sed 's/^/      /' <<<"$tracked_dirty"
fi
if [ -z "$untracked_release" ]; then
  ok "no untracked release-affecting files"
else
  bad "untracked release-affecting files exist"
  sed 's/^/      /' <<<"$untracked_release"
fi

section "Local Artifacts"
for p in "$APP_PATH" "$DMG_PATH" "$ZIP_PATH" "$DIST/agents" "$DIST/SHA256SUMS"; do
  if [ -e "$p" ]; then ok "found ${p#$ROOT/}"; else bad "missing ${p#$ROOT/}"; fi
done

if [ -x "$HELPER_PATH" ]; then
  version_out="$("$HELPER_PATH" version 2>/dev/null || true)"
  if [[ "$version_out" == "agent-observatory $VERSION" ]]; then
    ok "bundled helper version is $VERSION"
  else
    bad "bundled helper version mismatch: ${version_out:-<no output>}"
  fi
else
  bad "bundled helper is not executable"
fi

dist_app_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
dist_ext_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP_PATH/Contents/Library/SystemExtensions/com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension.systemextension/Contents/Info.plist" 2>/dev/null || true)"
if [ -n "$EXPECTED_BUILD_VERSION" ] &&
   [ "$dist_app_version" = "$EXPECTED_BUILD_VERSION" ] &&
   [ "$dist_ext_version" = "$EXPECTED_BUILD_VERSION" ]; then
  ok "dist app and system extension build match project build $EXPECTED_BUILD_VERSION"
else
  bad "dist build does not match project build (project=${EXPECTED_BUILD_VERSION:-unknown}, app=${dist_app_version:-unknown}, ext=${dist_ext_version:-unknown})"
fi

if [ -d "$APP_PATH" ] && codesign --verify --deep --strict "$APP_PATH" >/dev/null 2>&1; then
  ok "dist app codesign verifies"
else
  bad "dist app codesign verification failed"
fi

if [ -f "$DMG_PATH" ] && hdiutil verify "$DMG_PATH" >/dev/null 2>&1; then
  ok "DMG checksum/structure verifies"
else
  bad "DMG checksum/structure verification failed"
fi

if [ -f "$DIST/SHA256SUMS" ] && (cd "$DIST" && shasum -a 256 -c SHA256SUMS >/dev/null 2>&1); then
  ok "SHA256SUMS match local artifacts"
else
  bad "SHA256SUMS do not match local artifacts"
fi

section "Notarization"
if [ -d "$APP_PATH" ] && spctl -a -vvv "$APP_PATH" >/dev/null 2>&1 && xcrun stapler validate "$APP_PATH" >/dev/null 2>&1; then
  ok "dist app is Gatekeeper-accepted and stapled"
else
  bad "dist app is not yet Gatekeeper-accepted + stapled"
fi

if [ -f "$DMG_PATH" ] && spctl -a -vvv -t open --context context:primary-signature "$DMG_PATH" >/dev/null 2>&1 && xcrun stapler validate "$DMG_PATH" >/dev/null 2>&1; then
  ok "dist DMG is Gatekeeper-accepted and stapled"
else
  bad "dist DMG is not yet Gatekeeper-accepted + stapled"
fi

section "Installed App And NE State"
if [ -x "$INSTALLED_HELPER" ]; then
  ok "installed helper exists in /Applications"
  if [ -x "$HELPER_PATH" ]; then
    dist_helper_hash="$(shasum -a 256 "$HELPER_PATH" | awk '{print $1}')"
    installed_helper_hash="$(shasum -a 256 "$INSTALLED_HELPER" | awk '{print $1}')"
    if [ "$dist_helper_hash" = "$installed_helper_hash" ]; then
      ok "installed helper matches dist helper"
    else
      bad "installed helper does not match dist helper"
    fi
  fi
  dist_app_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
  installed_app_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$INSTALLED_APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
  if [ -n "$dist_app_version" ] && [ "$dist_app_version" = "$installed_app_version" ]; then
    ok "installed app bundle version matches dist ($installed_app_version)"
  else
    bad "installed app bundle version does not match dist (dist=${dist_app_version:-unknown}, installed=${installed_app_version:-unknown})"
  fi
  DIST_EXT="$APP_PATH/Contents/Library/SystemExtensions/com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension.systemextension"
  INSTALLED_EXT="$INSTALLED_APP_PATH/Contents/Library/SystemExtensions/com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension.systemextension"
  if [ -x "$DIST_EXT/Contents/MacOS/TransparentProxyExtension" ] && [ -x "$INSTALLED_EXT/Contents/MacOS/TransparentProxyExtension" ]; then
    dist_ext_hash="$(shasum -a 256 "$DIST_EXT/Contents/MacOS/TransparentProxyExtension" | awk '{print $1}')"
    installed_ext_hash="$(shasum -a 256 "$INSTALLED_EXT/Contents/MacOS/TransparentProxyExtension" | awk '{print $1}')"
    if [ "$dist_ext_hash" = "$installed_ext_hash" ]; then
      ok "installed system extension binary matches dist"
    else
      bad "installed system extension binary does not match dist"
    fi
  else
    bad "cannot compare installed system extension binary with dist"
  fi
  status_out="$("$INSTALLED_HELPER" status 2>&1 || true)"
  if grep -q 'overall: installed' <<<"$status_out"; then
    ok "installed helper reports overall: installed"
  else
    bad "installed helper does not report overall: installed"
  fi
  trust_out="$("$INSTALLED_HELPER" trust status 2>&1 || true)"
  if grep -q 'CA is trusted in the login keychain' <<<"$trust_out"; then
    ok "current local CA is trusted in the login keychain"
  else
    bad "current local CA is not trusted in the login keychain"
  fi
else
  bad "installed helper missing from /Applications"
fi

if have curl && curl -fsS http://127.0.0.1:7878/healthz >/dev/null 2>&1; then
  ok "ambient daemon healthz responds"
else
  bad "ambient daemon healthz does not respond on 127.0.0.1:7878"
fi

if have systemextensionsctl; then
  se_out="$(systemextensionsctl list 2>/dev/null || true)"
  obs_lines="$(grep 'agentobservatory' <<<"$se_out" || true)"
  current_build_lines="$(grep 'agentobservatory' <<<"$se_out" | grep -F "(${VERSION}/${EXPECTED_BUILD_VERSION})" || true)"
  current_active_enabled_count="$(grep -c '\[activated enabled\]' <<<"$current_build_lines" || true)"
  current_waiting_count="$(grep -Ec 'waiting|terminated waiting' <<<"$current_build_lines" || true)"
  waiting_count="$(grep 'agentobservatory' <<<"$se_out" | grep -Ec 'waiting|terminated waiting' || true)"
  if [ "$current_active_enabled_count" -eq 1 ] && [ "$waiting_count" -eq 0 ]; then
    ok "current Observatory system extension build is activated/enabled"
  elif [ "$current_active_enabled_count" -eq 1 ] && [ "$current_waiting_count" -eq 0 ] && [ "$waiting_count" -gt 0 ]; then
    note "current Observatory build is active; older system-extension tombstone remains"
    sed 's/^/      /' <<<"$obs_lines"
  else
    bad "Observatory system-extension state is not clean"
    if [ -n "$obs_lines" ]; then
      sed 's/^/      /' <<<"$obs_lines"
    else
      printf "      no Observatory extension lines found\n"
    fi
  fi
else
  note "systemextensionsctl unavailable"
fi

section "Remote Release State"
head_sha="$(git rev-parse HEAD 2>/dev/null || true)"
origin_ne="$(git rev-parse origin/ne-epic 2>/dev/null || true)"
origin_main="$(git rev-parse origin/main 2>/dev/null || true)"
ahead_ne="$(git rev-list --count origin/ne-epic..HEAD 2>/dev/null || echo unknown)"
ahead_main="$(git rev-list --count origin/main..HEAD 2>/dev/null || echo unknown)"

if [ "$ahead_ne" = "0" ] || [ "$ahead_main" = "0" ]; then
  ok "current commit appears pushed to at least one tracked release branch"
else
  bad "current commit is not pushed (ahead origin/ne-epic=$ahead_ne, ahead origin/main=$ahead_main)"
fi

if have gh; then
  v02_json="$(gh release view v0.2.0 --repo cipher982/agent-observatory --json name,assets,isDraft,targetCommitish,tagName 2>/dev/null || true)"
  if [ -z "$v02_json" ]; then
    bad "v0.2.0 release does not exist"
  else
    ok "v0.2.0 release exists"
    if grep -q "Agent Observatory" <<<"$v02_json"; then
      ok "v0.2.0 release name uses Agent Observatory"
    else
      bad "v0.2.0 release name does not use Agent Observatory"
    fi

    release_is_draft="$(gh release view v0.2.0 --repo cipher982/agent-observatory --json isDraft --jq '.isDraft' 2>/dev/null || true)"
    release_target="$(gh release view v0.2.0 --repo cipher982/agent-observatory --json targetCommitish --jq '.targetCommitish' 2>/dev/null || true)"
    if [ "$release_is_draft" = "true" ]; then
      resolved_target="$(git rev-parse "$release_target" 2>/dev/null || true)"
      if [ "$release_target" = "$head_sha" ] || [ "$resolved_target" = "$head_sha" ]; then
        ok "v0.2.0 draft release targets current commit"
      else
        bad "v0.2.0 draft release does not target current commit"
      fi
    else
      tag_sha="$(git ls-remote origin "refs/tags/v0.2.0^{}" "refs/tags/v0.2.0" 2>/dev/null | awk 'END {print $1}')"
      if [ -n "$tag_sha" ] && [ "$tag_sha" = "$head_sha" ]; then
        ok "v0.2.0 tag points at current commit"
      else
        bad "v0.2.0 tag does not point at current commit"
      fi
    fi

    if [ "$release_is_draft" = "true" ]; then
      note "v0.2.0 release is staged as a draft, not published"
    else
      ok "v0.2.0 release is published"
    fi

    if [ -f "$DIST/SHA256SUMS" ]; then
      dmg_sha="$(awk '/Agent-Observatory-.*-macOS[.]dmg$/ {print $1}' "$DIST/SHA256SUMS")"
      zip_sha="$(awk '/Agent-Observatory-.*-macOS[.]zip$/ {print $1}' "$DIST/SHA256SUMS")"
      bin_sha="$(awk '/agents$/ {print $1}' "$DIST/SHA256SUMS")"
      sums_sha="$(shasum -a 256 "$DIST/SHA256SUMS" | awk '{print $1}')"
      if grep -q "sha256:$dmg_sha" <<<"$v02_json" &&
         grep -q "sha256:$zip_sha" <<<"$v02_json" &&
         grep -q "sha256:$bin_sha" <<<"$v02_json" &&
         grep -q "sha256:$sums_sha" <<<"$v02_json"; then
        ok "v0.2.0 release assets match current local artifacts"
      else
        bad "v0.2.0 release assets do not match current local artifacts"
      fi
    else
      bad "cannot compare v0.2.0 release assets without dist/SHA256SUMS"
    fi
  fi
else
  note "gh unavailable; release state not checked"
fi

section "Summary"
printf "passes=%d warnings=%d failures=%d\n" "$pass" "$warn" "$fail"
if [ "$fail" -eq 0 ]; then
  printf "v0.2 readiness audit: PASS\n"
  exit 0
fi
printf "v0.2 readiness audit: FAIL\n"
exit 1
