#!/bin/sh
set -e

# agmon installer — downloads the latest release binary from GitHub

REPO="tt-a1i/agmon"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Get latest release tag
LATEST=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)

if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest release"
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${LATEST}/agmon_${OS}_${ARCH}.tar.gz"
echo "Downloading agmon ${LATEST} for ${OS}/${ARCH}..."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -sL "$URL" -o "$TMP/agmon.tar.gz"
tar -xzf "$TMP/agmon.tar.gz" -C "$TMP"

if [ ! -f "$TMP/agmon" ]; then
  echo "Error: binary not found in archive"
  exit 1
fi

chmod +x "$TMP/agmon"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/agmon" "$INSTALL_DIR/agmon"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv "$TMP/agmon" "$INSTALL_DIR/agmon"
fi

echo "agmon ${LATEST} installed to ${INSTALL_DIR}/agmon"
echo ""
echo "Get started:"
echo "  agmon setup    # configure Claude Code hooks"
echo "  agmon          # launch dashboard"
