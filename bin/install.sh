#!/bin/sh
set -e

REPO="circlesac/nosnitch-cli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
esac

if [ "$OS" != "darwin" ]; then
  echo "nosnitch supports macOS only (it reads macOS Keychain / Safari cookies)."
  exit 1
fi

VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
URL="https://github.com/$REPO/releases/download/$VERSION/nosnitch-$OS-$ARCH.tar.gz"

echo "Installing nosnitch $VERSION to $INSTALL_DIR…"
curl -fsSL "$URL" | tar xz -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/nosnitch"
echo "Installed. Run: nosnitch"
