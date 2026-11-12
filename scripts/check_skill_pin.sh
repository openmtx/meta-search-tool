#!/usr/bin/env bash
#
# Enforce "skill pin == release tag" (v0.5 release invariant).
#
# The web-search skill installs an exact mst release. If the release workflow
# publishes a tag while setup.sh still pins an older version, users who install
# via the skill silently keep running stale code. Run this before building or
# publishing on a tag push.
#
# Usage:
#   ./scripts/check_skill_pin.sh [TAG]
#
# With no TAG, uses $GITHUB_REF_NAME. On a non-tag/manual CI run there is no
# release tag to compare against, so the check logs and exits 0.
set -euo pipefail

setup="skills/web-search/scripts/setup.sh"
explicit_tag="${1:-}"
tag="${explicit_tag:-${GITHUB_REF_NAME:-}}"

if [ ! -f "$setup" ]; then
  echo "check_skill_pin: missing $setup" >&2
  exit 1
fi

line="$(grep -E '^[[:space:]]*ensure_tool[[:space:]]+mst[[:space:]]' "$setup" | head -n1 || true)"
if [ -z "$line" ]; then
  echo "check_skill_pin: no 'ensure_tool mst ...' line found in $setup" >&2
  exit 1
fi

# ensure_tool NAME REPO TAG SUMS_FILE RANGE
read -r _ name repo pin sums range <<< "$line"
if [ -z "${pin:-}" ]; then
  echo "check_skill_pin: could not parse the mst pin from: $line" >&2
  exit 1
fi
case "$pin" in
  v*) ;;
  *) echo "check_skill_pin: mst pin must be an exact v* tag, got '$pin' in $setup" >&2; exit 1 ;;
esac

if [ -z "$explicit_tag" ] && [ "${GITHUB_REF_TYPE:-}" != "tag" ]; then
  echo "check_skill_pin: no release tag (GITHUB_REF_TYPE=${GITHUB_REF_TYPE:-unset}); skipping pin check (pin=$pin)"
  exit 0
fi

if [ "$pin" != "$tag" ]; then
  echo "check_skill_pin: SKILL PIN MISMATCH" >&2
  echo "  release tag: $tag" >&2
  echo "  skill pin:   $pin" >&2
  echo "  file:        $setup" >&2
  echo "  fix: update the 'ensure_tool mst ...' line to $tag (and run the skill install test)" >&2
  exit 1
fi

echo "check_skill_pin: OK ($pin == $tag)"
