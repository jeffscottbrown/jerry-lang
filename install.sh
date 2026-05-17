#!/usr/bin/env bash
# Jerry language installer
# Usage:  curl -fsSL https://raw.githubusercontent.com/jeffscottbrown/jerry-lang/main/install.sh | bash
#         JERRY_VERSION=v0.0.3 bash install.sh   (pin a version)
#         JERRY_INSTALL_DIR=/usr/local/bin bash install.sh   (custom install dir)

set -euo pipefail

REPO="jeffscottbrown/jerry-lang"
INSTALL_DIR="${JERRY_INSTALL_DIR:-}"
VERSION="${JERRY_VERSION:-}"

# ── Helpers ───────────────────────────────────────────────────────────────────

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()    { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
err()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || err "required tool not found: $1"
}

need curl
need tar

# ── Detect OS and architecture ────────────────────────────────────────────────

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)  OS_KEY="linux" ;;
  Darwin) OS_KEY="macos" ;;
  *)      err "Unsupported OS: $OS. Build from source: https://github.com/$REPO" ;;
esac

case "$ARCH" in
  x86_64)        ARCH_KEY="x86_64" ;;
  arm64|aarch64) ARCH_KEY="arm64" ;;
  *)             err "Unsupported architecture: $ARCH. Build from source: https://github.com/$REPO" ;;
esac

ASSET_NAME="jerry-${OS_KEY}-${ARCH_KEY}"

# ── Resolve version ───────────────────────────────────────────────────────────

if [ -z "$VERSION" ]; then
  info "Fetching latest release..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [ -n "$VERSION" ] || err "Could not determine latest version. Set JERRY_VERSION=vX.Y.Z"
fi

info "Installing jerry ${VERSION} (${OS_KEY}/${ARCH_KEY})"

# ── Resolve install directory ─────────────────────────────────────────────────

if [ -z "$INSTALL_DIR" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
  fi
fi

# ── Download, verify, and install ─────────────────────────────────────────────

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ARCHIVE_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET_NAME}.tar.gz"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

info "Downloading ${ASSET_NAME}.tar.gz..."
curl -fsSL "$ARCHIVE_URL" -o "$TMP/${ASSET_NAME}.tar.gz"

info "Verifying checksum..."
curl -fsSL "$CHECKSUMS_URL" -o "$TMP/checksums.txt"
(cd "$TMP" && grep "${ASSET_NAME}.tar.gz" checksums.txt | sha256sum --check --status 2>/dev/null \
  || shasum -a 256 --check --status <(grep "${ASSET_NAME}.tar.gz" checksums.txt)) \
  || err "Checksum verification failed. The download may be corrupted."

info "Installing to ${INSTALL_DIR}/jerry..."
tar -xzf "$TMP/${ASSET_NAME}.tar.gz" -C "$TMP"
chmod +x "$TMP/${ASSET_NAME}"
mv "$TMP/${ASSET_NAME}" "${INSTALL_DIR}/jerry"

# ── Verify ────────────────────────────────────────────────────────────────────

ok "jerry installed to ${INSTALL_DIR}/jerry"
"${INSTALL_DIR}/jerry" --version

# ── PATH hint ─────────────────────────────────────────────────────────────────

if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  printf '\n\033[1;33mNote:\033[0m Add %s to your PATH:\n' "$INSTALL_DIR"
  printf '  export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
fi
