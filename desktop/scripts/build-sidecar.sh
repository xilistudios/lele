#!/usr/bin/env bash
#
# build-sidecar.sh
#
# Builds the lele Go binary as a Tauri externalBin sidecar for the desktop app.
#
# The Go binary embeds the web frontend (cmd/lele/web/dist via go:embed) and a
# workspace (cmd/lele/workspace), so this script:
#   1. builds the web frontend (web/dist)
#   2. copies web/dist into cmd/lele/web/dist (the go:embed location)
#   3. cross-compiles cmd/lele into desktop/src-tauri/binaries/lele-<triple>
#
# Usage:
#   bash desktop/scripts/build-sidecar.sh [--target <triple>]
#
# The default target is the host triple (best-effort detection).
#
set -euo pipefail

# --- Resolve repo root -------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# --- Arguments ---------------------------------------------------------------
TARGET=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      TARGET="${2:-}"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      echo "Usage: $0 [--target <triple>]" >&2
      exit 1
      ;;
  esac
done

# --- Detect host target triple (best effort) --------------------------------
detect_host_triple() {
  if command -v rustc >/dev/null 2>&1; then
    local host
    host="$(rustc --version --verbose 2>/dev/null | sed -n 's/^host: //p' || true)"
    if [[ -n "${host}" ]]; then
      echo "${host}"
      return 0
    fi
  fi
  local arch
  arch="$(uname -m 2>/dev/null || echo unknown)"
  case "${arch}" in
    x86_64)
      echo "x86_64-unknown-linux-gnu"
      ;;
    aarch64 | arm64)
      echo "aarch64-unknown-linux-gnu"
      ;;
    *)
      echo "x86_64-unknown-linux-gnu"
      ;;
  esac
}

if [[ -z "${TARGET}" ]]; then
  TARGET="$(detect_host_triple)"
fi

echo "Building sidecar for target: ${TARGET}"

# --- Map triple -> GOOS/GOARCH ----------------------------------------------
case "${TARGET}" in
  x86_64-unknown-linux-gnu)  GOOS=linux;   GOARCH=amd64; EXE="" ;;
  aarch64-unknown-linux-gnu) GOOS=linux;   GOARCH=arm64; EXE="" ;;
  x86_64-apple-darwin)       GOOS=darwin;  GOARCH=amd64; EXE="" ;;
  aarch64-apple-darwin)      GOOS=darwin;  GOARCH=arm64; EXE="" ;;
  x86_64-pc-windows-gnu)     GOOS=windows; GOARCH=amd64; EXE=".exe" ;;
  x86_64-pc-windows-msvc)    GOOS=windows; GOARCH=amd64; EXE=".exe" ;;
  aarch64-pc-windows-msvc)   GOOS=windows; GOARCH=arm64; EXE=".exe" ;;
  *)
    echo "error: unsupported target triple: ${TARGET}" >&2
    exit 1
    ;;
esac

# --- Build the web frontend --------------------------------------------------
echo "Building web frontend..."
if command -v bun >/dev/null 2>&1; then
  (
    cd "${ROOT}/web"
    bun install --frozen-lockfile 2>/dev/null || bun install
    bun run build
  )
elif command -v npm >/dev/null 2>&1; then
  (
    cd "${ROOT}/web"
    npm ci 2>/dev/null || npm install
    npm run build
  )
else
  echo "error: neither bun nor npm is available to build the web frontend" >&2
  exit 1
fi

# --- Copy web/dist into the go:embed location -------------------------------
EMBED_WEB_DIR="${ROOT}/cmd/lele/web"
EMBED_WS_DIR="${ROOT}/cmd/lele/workspace"
echo "Copying web/dist to ${EMBED_WEB_DIR}/dist..."
rm -rf "${EMBED_WEB_DIR}/dist"
mkdir -p "${EMBED_WEB_DIR}"
cp -R "${ROOT}/web/dist" "${EMBED_WEB_DIR}/dist"

# Ensure the workspace dir exists for the go:embed directive.
mkdir -p "${EMBED_WS_DIR}"

# --- Build metadata ----------------------------------------------------------
VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# --- Cross-compile the Go sidecar -------------------------------------------
OUT_DIR="${ROOT}/desktop/src-tauri/binaries"
OUT_PATH="${OUT_DIR}/lele-${TARGET}${EXE}"
mkdir -p "${OUT_DIR}"

echo "Cross-compiling sidecar (${GOOS}/${GOARCH})..."
CGO_ENABLED=0 \
  GOOS="${GOOS}" \
  GOARCH="${GOARCH}" \
  go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o "${OUT_PATH}" \
    ./cmd/lele

# --- Summary ----------------------------------------------------------------
SIZE="$(du -h "${OUT_PATH}" | cut -f1)"
echo ""
echo "Sidecar build complete:"
echo "  target:   ${TARGET}"
echo "  binary:   ${OUT_PATH}"
echo "  size:     ${SIZE}"
echo "  version:  ${VERSION} (${GIT_COMMIT})"