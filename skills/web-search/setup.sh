#!/usr/bin/env bash
set -euo pipefail

# setup.sh — download mst binary for the current platform
#
# Downloads the latest release from GitHub, verifies the checksum, and places
# the binary in a local scripts directory. Adds that directory to PATH for the
# current shell.
#
# Usage:  source setup.sh
#         (source it so PATH export takes effect in your shell)

REPO="openmtx/meta-search-tool"
INSTALL_DIR="${MST_INSTALL_DIR:-$HOME/.local/share/mst/bin}"

# ---- detect platform --------------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64"  ;;
  aarch64|arm64) ARCH="arm64"  ;;
  *)             echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac

case "$OS" in
  linux|darwin) EXT="tar.gz"  ;;
  mingw*|msys*|cygwin*) OS="windows"; EXT="zip"   ;;
  windows)      EXT="zip"   ;;
  *)            echo "unsupported OS: $(uname -s)"; exit 1 ;;
esac

# ---- fetch latest release tag -----------------------------------------------
echo "fetching latest release …"
TAG="$(curl -sL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' | cut -d'"' -f4)"
if [ -z "$TAG" ]; then
  echo "error: could not determine latest release tag"
  exit 1
fi
echo "latest release: $TAG"

# ---- download archive + checksums -------------------------------------------
ARCHIVE="mst-${TAG}-${OS}-${ARCH}.${EXT}"
CHECKSUMS="SHA256SUMS.txt"

BASE_URL="https://github.com/$REPO/releases/download/$TAG"

mkdir -p "$INSTALL_DIR"

echo "downloading $ARCHIVE …"
curl -sL "${BASE_URL}/${ARCHIVE}" -o "${INSTALL_DIR}/${ARCHIVE}"

echo "downloading checksums …"
curl -sL "${BASE_URL}/${CHECKSUMS}" -o "${INSTALL_DIR}/${CHECKSUMS}"

# ---- verify checksum --------------------------------------------------------
echo "verifying checksum …"
(cd "$INSTALL_DIR" && sha256sum -c --ignore-missing "$CHECKSUMS" 2>/dev/null)
echo "checksum OK"

# ---- extract ----------------------------------------------------------------
echo "extracting …"
case "$EXT" in
  tar.gz) tar xzf "${INSTALL_DIR}/${ARCHIVE}" -C "$INSTALL_DIR"  ;;
  zip)    unzip -o "${INSTALL_DIR}/${ARCHIVE}" -d "$INSTALL_DIR" ;;
esac

# Locate the binary (it's inside a versioned directory in the archive).
BIN_PATH="$(find "$INSTALL_DIR" -name 'mst' -o -name 'mst.exe' | head -1)"
if [ -z "$BIN_PATH" ]; then
  echo "error: binary not found in extracted archive"
  exit 1
fi
chmod +x "$BIN_PATH"

# Copy to the install dir root so the path is predictable.
cp "$BIN_PATH" "${INSTALL_DIR}/mst"
[ "$OS" = "windows" ] && mv "${INSTALL_DIR}/mst" "${INSTALL_DIR}/mst.exe"

# Clean up downloaded artifacts.
rm -f "${INSTALL_DIR}/${ARCHIVE}" "${INSTALL_DIR}/${CHECKSUMS}"
rm -rf "${INSTALL_DIR}/mst-${TAG}-${OS}-${ARCH}"

# ---- add to PATH ------------------------------------------------------------
# shellcheck disable=SC2016
echo 'export PATH="$PATH:'"$INSTALL_DIR"'"'
export PATH="$PATH:$INSTALL_DIR"

echo ""
echo "mst $TAG installed to $INSTALL_DIR/mst"
echo ""
mst --version
