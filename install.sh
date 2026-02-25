#!/usr/bin/env bash
set -euo pipefail

REPO="flashbots/builder-playground"
BIN="builder-playground"
API="https://api.github.com/repos/${REPO}/releases/latest"

# Detect OS
OS="$(uname | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect ARCH
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

if [ -n "${VERSION:-}" ]; then
  # Normalize: ensure tag has a "v" prefix
  TAG="${VERSION#v}"
  TAG="v${TAG}"
  echo "Checking version: $TAG"
  if ! curl -sSfL "https://api.github.com/repos/${REPO}/releases/tags/${TAG}" > /dev/null 2>&1; then
    echo "Error: version '${TAG}' not found. Check available releases at https://github.com/${REPO}/releases"
    exit 1
  fi
else
  echo "Fetching latest release..."
  TAG=$(curl -sSfL "$API" | grep -oP '"tag_name": "\K(.*)(?=")')
  echo "Latest version: $TAG"
fi

ASSET="${BIN}_${TAG}_${OS}_${ARCH}.zip"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

echo "Downloading $ASSET..."
curl -sSfL "$URL" -o "$ASSET"

echo "Extracting..."
unzip -o "$ASSET"

echo "Installing to /usr/local/bin..."
chmod +x "$BIN"
sudo mv "$BIN" /usr/local/bin/

echo "Cleaning up..."
rm -f "$ASSET"

echo "✅ Installed $BIN $TAG for ${OS}-${ARCH}"
