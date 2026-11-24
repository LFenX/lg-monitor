#!/bin/sh
# Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor
# Install or upgrade lgmon on Linux (amd64 / arm64).
# Usage:
#   curl -fsSL https://github.com/LFenX/lg-monitor/releases/latest/download/install.sh | sudo sh
#   curl -fsSL https://github.com/LFenX/lg-monitor/releases/latest/download/install.sh | sh -s -- --no-service

set -eu

REPO="LFenX/lg-monitor"
INSTALL_DIR="${LGMON_INSTALL_DIR:-/usr/local/bin}"
VERSION="${LGMON_VERSION:-latest}"
NO_SERVICE=0

for arg in "$@"; do
  case "$arg" in
    --no-service) NO_SERVICE=1 ;;
    --dir=*) INSTALL_DIR="${arg#--dir=}" ;;
    --version=*) VERSION="${arg#--version=}" ;;
    -h|--help)
      echo "Usage: install.sh [--no-service] [--dir=/usr/local/bin] [--version=v0.18.0]"
      exit 0
      ;;
  esac
done

OS="$(uname -s)"
if [ "$OS" != "Linux" ]; then
  echo "lgmon install.sh is for Linux (detected: $OS). On Windows, download lgmon-setup.exe from GitHub Releases." >&2
  exit 1
fi

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported Linux architecture: $ARCH_RAW (supported: amd64, arm64)" >&2
    exit 1
    ;;
esac

ASSET="lgmon-linux-${ARCH}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
else
  case "$VERSION" in
    v*) ;;
    *) VERSION="v${VERSION}" ;;
  esac
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
fi

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    echo "Please install curl or wget first." >&2
    exit 1
  fi
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo ""
  fi
}

TMPDIR_PATH="$(mktemp -d)"
cleanup() { rm -rf "$TMPDIR_PATH"; }
trap cleanup EXIT INT TERM

echo "Downloading ${ASSET}..."
fetch "${BASE_URL}/${ASSET}" "${TMPDIR_PATH}/${ASSET}"
fetch "${BASE_URL}/SHA256SUMS" "${TMPDIR_PATH}/SHA256SUMS"

EXPECTED="$(awk -v name="$ASSET" '$2 == name || $2 == "*"name {print tolower($1); exit}' "${TMPDIR_PATH}/SHA256SUMS")"
if [ -z "$EXPECTED" ]; then
  echo "Checksum for ${ASSET} not found in SHA256SUMS." >&2
  exit 1
fi
ACTUAL="$(sha256_file "${TMPDIR_PATH}/${ASSET}" | tr '[:upper:]' '[:lower:]')"
if [ -n "$ACTUAL" ] && [ "$ACTUAL" != "$EXPECTED" ]; then
  echo "SHA-256 mismatch for ${ASSET}: expected ${EXPECTED}, got ${ACTUAL}" >&2
  exit 1
fi

tar -xzf "${TMPDIR_PATH}/${ASSET}" -C "${TMPDIR_PATH}"
BIN_PATH="$(find "${TMPDIR_PATH}" -type f -name lgmon | head -n 1)"
if [ -z "$BIN_PATH" ]; then
  echo "Archive did not contain lgmon binary." >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
TARGET="${INSTALL_DIR}/lgmon"
STAGE="${INSTALL_DIR}/.lgmon-install.$$"
cp "$BIN_PATH" "$STAGE"
chmod 0755 "$STAGE"
mv -f "$STAGE" "$TARGET"

echo "Installed lgmon to ${TARGET}: $("${TARGET}" version 2>/dev/null || true)"

if [ "$NO_SERVICE" -eq 0 ] && [ "$(id -u)" -eq 0 ] && command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet lgmon.service 2>/dev/null; then
    echo "Restarting running lgmon.service..."
    "${TARGET}" service restart
  else
    echo "Registering and starting lgmon.service..."
    "${TARGET}" install
    "${TARGET}" service start
  fi
  echo ""
  echo "lgmon is running on this Linux machine (default panel: http://127.0.0.1:7444)."
  echo "Open from another computer via SSH tunnel:"
  echo "  ssh -L 7444:127.0.0.1:7444 <user>@<this-host>"
  echo "Then open http://127.0.0.1:7444 in your browser and sign in with your lgmon account to sync with Windows."
else
  echo ""
  echo "Run interactively with: lgmon run"
  echo "Or install as a systemd service with: sudo lgmon install && sudo lgmon service start"
fi
