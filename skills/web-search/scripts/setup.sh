#!/usr/bin/env bash
#
# web-search skill — find-or-install mst into the per-skill cache.
#
# Pins an EXACT tested version (bumped only after re-testing) and SHA256-verifies
# the download. mst is NOT installed on PATH — it lives at
#   ~/.cache/agent-skills/web-search/bin/mst
# so it never clashes with another skill's mst. See ../DEPENDENCIES.md.
#
# Cross-harness: SKILL.md runs this via its stable raw URL, so the agent never
# needs to know the skill directory:
#   curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/scripts/setup.sh | bash
#
# Prints the binary path to stdout (logs to stderr), so:  MST="$(... | bash)"
#
set -euo pipefail

SKILL="web-search"
REPO="openmtx/meta-search-tool"

# --- pinned version + compatibility range -----------------------------------
# VERSION is the exact release this skill was tested against — a new upstream
# release never auto-replaces it. REQUIRES_MST is the compatibility range used
# only for the drift-check (keep these in sync with SKILL.md metadata.requires.mst).
VERSION="v0.3.2"
REQUIRES_MST=">=0.3,<1.0"

# --- cache location ---------------------------------------------------------
CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}/agent-skills"
BIN_DIR="$CACHE_ROOT/$SKILL/bin"
BIN="$BIN_DIR/mst"

# --- OS/arch → release asset ------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) ;; esac
case "$OS" in
  mingw*|msys*|cygwin*) echo "setup.sh supports macOS/Linux; on Windows install mst manually" >&2; exit 1;;
esac
ASSET="mst-${VERSION}-${OS}-${ARCH}.tar.gz"

log() { echo "mst-skill: $*" >&2; }
die() { echo "mst-skill: ERROR: $*" >&2; exit 1; }

# sha256 tool: Linux = sha256sum, macOS = shasum -a 256
if   command -v sha256sum >/dev/null 2>&1; then SUM=(sha256sum)
elif command -v shasum    >/dev/null 2>&1; then SUM=(shasum -a 256)
else die "no sha256 tool (sha256sum / shasum) found"; fi

# version_satisfies VER ">=min,<max"  → 0 if VER is within [min, max)
version_satisfies() {
  local ver="$1" range="$2" min max
  min="${range#*>=}"; min="${min%%,*}"     # ">=0.3,<1.0" → "0.3"
  max="${range#*,<}"                        #              → "1.0"
  awk -v ver="${ver#v}" -v min="${min#v}" -v max="${max#v}" 'BEGIN{
    nv=split(ver,va,"."); nm=split(min,ma,"."); nx=split(max,mb,".");
    cmp_min=0; for(i=1;i<=nv||i<=nm;i++){x=(i<=nv?va[i]+0:0);y=(i<=nm?ma[i]+0:0); if(x>y){cmp_min=1;break} if(x<y){cmp_min=-1;break}}
    cmp_max=0; for(i=1;i<=nv||i<=nx;i++){x=(i<=nv?va[i]+0:0);y=(i<=nx?mb[i]+0:0); if(x>y){cmp_max=1;break} if(x<y){cmp_max=-1;break}}
    exit (cmp_min>=0 && cmp_max<0) ? 0 : 1
  }'
}

mkdir -p "$BIN_DIR"

# 1. find-or-reuse
if [ -x "$BIN" ]; then
  INSTALLED="$("$BIN" --version 2>/dev/null | awk '{print $2}' || true)"
  if [ "$INSTALLED" = "$VERSION" ]; then
    log "mst $VERSION already installed at $BIN"
    echo "$BIN"; exit 0
  fi
  # 2. drift-check: keep a different version only if it still satisfies the range
  if [ -n "$INSTALLED" ] && version_satisfies "$INSTALLED" "$REQUIRES_MST"; then
    log "using existing mst $INSTALLED at $BIN (within $REQUIRES_MST)"
    echo "$BIN"; exit 0
  fi
  log "cached mst '${INSTALLED:-unknown}' out of range ($REQUIRES_MST); reinstalling $VERSION"
fi

# 3. install the pinned release
log "installing mst $VERSION → $BIN"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BASE="https://github.com/$REPO/releases/download/$VERSION"
curl -fsSL "$BASE/$ASSET"          -o "$TMP/$ASSET"         || die "download failed: $ASSET"
curl -fsSL "$BASE/SHA256SUMS.txt"  -o "$TMP/SHA256SUMS.txt" || die "download failed: SHA256SUMS.txt"

# 3b. SHA256-verify against the release's sums (an unverified artifact is never installed)
grep -F "$ASSET" "$TMP/SHA256SUMS.txt" > "$TMP/sum.line" || die "no SHA256 sum for $ASSET"
( cd "$TMP" && "${SUM[@]}" -c sum.line ) >/dev/null || die "SHA256 mismatch for $ASSET"

# 3c. extract + atomic place (temp → mv onto final path; concurrent runs never see a half write)
tar xzf "$TMP/$ASSET" -C "$TMP"
BINARY="$(find "$TMP" -maxdepth 2 -type f -name mst 2>/dev/null)"
BINARY="${BINARY%%$'\n'*}"   # first match if several
[ -n "$BINARY" ] || die "mst binary not found in archive"
mv "$BINARY" "$BIN.tmp"
chmod 0755 "$BIN.tmp"
mv "$BIN.tmp" "$BIN"

log "installed mst $VERSION → $BIN"
echo "$BIN"
