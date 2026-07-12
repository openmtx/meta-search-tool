#!/usr/bin/env bash
#
# web-search skill — find-or-install mst + webclaw into ~/.local/bin/.
#
# Each tool is pinned to an exact tested version and SHA256-verified. This is
# the single source of truth for what the skill needs.
#
# Cross-harness:
#   curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/scripts/setup.sh | bash
#
# Prints the bin dir to stdout (logs to stderr).
#
set -euo pipefail

BIN_DIR="${HOME}/.local/bin"

# --- OS/arch ----------------------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) ;; esac
case "$OS" in mingw*|msys*|cygwin*) echo "setup.sh supports macOS/Linux; on Windows install the tools manually" >&2; exit 1;; esac

log() { echo "web-search-skill: $*" >&2; }
die() { echo "web-search-skill: ERROR: $*" >&2; exit 1; }

if   command -v sha256sum >/dev/null 2>&1; then SUM=(sha256sum)
elif command -v shasum    >/dev/null 2>&1; then SUM=(shasum -a 256)
else die "no sha256 tool (sha256sum / shasum) found"; fi

stripv() { printf '%s' "${1#v}"; }

# version_satisfies VER ">=min,<max"  → 0 if VER is within [min, max)
version_satisfies() {
  local range="$2" min max
  min="${range#*>=}"; min="${min%%,*}"
  max="${range#*,<}"
  awk -v ver="$(stripv "$1")" -v min="$(stripv "$min")" -v max="$(stripv "$max")" 'BEGIN{
    nv=split(ver,va,"."); nm=split(min,ma,"."); nx=split(max,mb,".");
    cm=0; for(i=1;i<=nv||i<=nm;i++){x=(i<=nv?va[i]+0:0);y=(i<=nm?ma[i]+0:0); if(x>y){cm=1;break} if(x<y){cm=-1;break}}
    cx=0; for(i=1;i<=nv||i<=nx;i++){x=(i<=nv?va[i]+0:0);y=(i<=nx?mb[i]+0:0); if(x>y){cx=1;break} if(x<y){cx=-1;break}}
    exit (cm>=0 && cx<0) ? 0 : 1
  }'
}

# asset_name NAME TAG → release archive filename for this OS/arch
asset_name() {
  local name="$1" tag="$2" triple=""
  if [ "$name" = "mst" ]; then
    echo "mst-${tag}-${OS}-${ARCH}.tar.gz"; return
  fi
  case "$OS" in
    darwin) case "$ARCH" in amd64) triple="x86_64-apple-darwin";;   arm64) triple="aarch64-apple-darwin";;   esac;;
    linux)  case "$ARCH" in amd64) triple="x86_64-unknown-linux-gnu";; arm64) triple="aarch64-unknown-linux-gnu";; esac;;
  esac
  [ -n "$triple" ] || die "unsupported OS/arch for $name: ${OS}/${ARCH}"
  echo "webclaw-${tag}-${triple}.tar.gz"
}

# ensure_tool NAME REPO TAG SUMS_FILE RANGE  →  ensures $BIN_DIR/NAME exists
ensure_tool() {
  local name="$1" repo="$2" tag="$3" sums="$4" range="$5"
  local bin="$BIN_DIR/$name"

  if [ -x "$bin" ]; then
    local inst; inst="$("$bin" --version 2>/dev/null | awk '{print $2}' || true)"
    if [ "$(stripv "$inst")" = "$(stripv "$tag")" ]; then
      log "$name $(stripv "$tag") already installed at $bin"; return 0
    fi
    if [ -n "$inst" ] && version_satisfies "$inst" "$range"; then
      log "using existing $name $inst at $bin (within $range)"; return 0
    fi
    log "existing $name '${inst:-unknown}' out of range ($range); reinstalling $(stripv "$tag")"
  else
    log "installing $name $(stripv "$tag") → $bin"
  fi

  local asset; asset="$(asset_name "$name" "$tag")"
  local base="https://github.com/$repo/releases/download/$tag"
  curl -fsSL "$base/$asset" -o "$TMP/$asset"   || die "$name: download failed: $asset"
  curl -fsSL "$base/$sums"  -o "$TMP/$sums"    || die "$name: download failed: $sums"
  grep -F "$asset" "$TMP/$sums" > "$TMP/sum.line" || die "$name: no SHA256 sum for $asset in $sums"
  ( cd "$TMP" && "${SUM[@]}" -c sum.line ) >/dev/null || die "$name: SHA256 mismatch for $asset"
  tar xzf "$TMP/$asset" -C "$TMP"
  local binary; binary="$(find "$TMP" -maxdepth 2 -type f -name "$name" 2>/dev/null)"
  binary="${binary%%$'\n'*}"
  [ -n "$binary" ] || die "$name: binary not found in archive"
  mv "$binary" "$bin.tmp"
  chmod 0755 "$bin.tmp"
  mv "$bin.tmp" "$bin"
  log "installed $name $(stripv "$tag") → $bin"
}

# --- one scratch dir for the whole run, cleaned on any exit -----------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$BIN_DIR"
#                NAME     REPO                        TAG      SUMS_FILE       RANGE
ensure_tool     mst      openmtx/meta-search-tool    v0.3.2   SHA256SUMS.txt  ">=0.3,<1.0"
ensure_tool     webclaw  0xMassi/webclaw             v0.6.14  SHA256SUMS      ">=0.6,<1.0"

# Ensure ~/.local/bin is on PATH for the current shell
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) log "WARNING: $BIN_DIR is not on your PATH. Add it to your shell config:" >&2
     log "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc" >&2
     log "  source ~/.bashrc" >&2
     ;;
esac

echo "$BIN_DIR"
