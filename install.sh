#!/bin/sh
# Lele universal installer
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/xilistudios/lele/main/install.sh | sh
#   curl -fsSL ... | sh -s -- --version v0.2.0
#   curl -fsSL ... | sh -s -- --prefix /usr/local
set -eu

REPO="xilistudios/lele"
BINARY="lele"
DEFAULT_PREFIX="${HOME}/.local"

# Pinned release signing keys (ed25519, PKIX PEM, one block per key). The
# release workflow signs lele_<version>_checksums.txt with the matching private
# key and publishes the detached raw signature as
# lele_<version>_checksums.txt.sig. The signature is accepted when ANY pinned
# key verifies it, so a key rotation can pin the new key next to the old one and
# keep installing during the overlap window. The first key must be the one
# pinned in pkg/update/release_signing_key.go; see docs/release-signing.md.
RELEASE_SIGNING_PUBKEYS_PEM='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAybtp/SDuO9p/gqHXUBrdORoVDwmDwUiM8NmsxwbPd5o=
-----END PUBLIC KEY-----'

# ── Helpers ──────────────────────────────────────────────────────────

log()   { printf '  %s\n' "$@"; }
info()  { printf '\033[1;34m==> %s\033[0m\n' "$@"; }
ok()    { printf '\033[1;32m==> %s\033[0m\n' "$@"; }
err()   { printf '\033[1;31mError: %s\033[0m\n' "$@" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || err "'$1' is required but not found."
}

# sha256 is what makes the download trustworthy, so a host without a hashing
# tool is rejected before anything is downloaded.
detect_hash_tool() {
  if command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    echo "shasum"
  else
    err "sha256sum or shasum is required to verify the download; install one and retry."
  fi
}

hash_file() {
  case "$HASH_TOOL" in
    sha256sum) sha256sum "$1" | awk '{print $1}' ;;
    shasum)    shasum -a 256 "$1" | awk '{print $1}' ;;
    *)         err "sha256sum or shasum is required to verify the download; install one and retry." ;;
  esac
}

http_get() {
  url="$1"; dst="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dst" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dst" "$url"
  else
    err "curl or wget is required."
  fi
}

http_get_stdout() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url"
  else
    err "curl or wget is required."
  fi
}

# fetch_asset downloads URL into DST and reports how the request itself went:
#   0  the body was written to DST
#   2  the asset does not exist (HTTP 404/410)
#   1  transport failure (DNS, connection, TLS, timeout, server error)
fetch_asset() {
  url="$1"; dst="$2"
  if command -v curl >/dev/null 2>&1; then
    code=$(curl -sSL -o "$dst" -w '%{http_code}' "$url" 2>/dev/null) || return 1
    case "$code" in
      2??) return 0 ;;
      404|410) return 2 ;;
      *) return 1 ;;
    esac
  elif command -v wget >/dev/null 2>&1; then
    headers=$(wget -qS -O "$dst" "$url" 2>&1) && return 0
    case "$headers" in
      *" 404 "*|*" 410 "*) return 2 ;;
      *) return 1 ;;
    esac
  fi
  err "curl or wget is required."
}

# fetch_signature is fetch_asset with retries: a transport failure is retried
# twice, a missing asset is reported immediately.
fetch_signature() {
  attempt=1
  while [ "$attempt" -le 3 ]; do
    rc=0
    fetch_asset "$1" "$2" || rc=$?
    if [ "$rc" -eq 0 ]; then return 0; fi
    if [ "$rc" -eq 2 ]; then return 2; fi
    attempt=$((attempt + 1))
    if [ "$attempt" -le 3 ]; then sleep 1; fi
  done
  return 1
}

# Ed25519 oneshot verification ("pkeyutl -rawin") exists in OpenSSL >= 3.0 only.
# An older or non-OpenSSL build (LibreSSL, BoringSSL) fails on EVERY signature,
# valid or not, so its verdict must never be read as "tampered".
openssl_can_verify_rawin() {
  openssl pkeyutl -help 2>&1 | grep -q -- '-rawin'
}

# verify_pinned_signature DATA SIG succeeds when any pinned key verifies the
# signature over DATA.
verify_pinned_signature() {
  data="$1"; sig="$2"
  remaining="$RELEASE_SIGNING_PUBKEYS_PEM"
  index=0
  status=1
  while :; do
    block="${remaining%%-----END PUBLIC KEY-----*}"
    if [ "$block" = "$remaining" ]; then break; fi
    remaining="${remaining#*-----END PUBLIC KEY-----}"
    index=$((index + 1))
    keyfile="${TMPDIR}/release-signing-key-${index}.pem"
    printf '%s\n' "${block}-----END PUBLIC KEY-----" > "$keyfile"
    if openssl pkeyutl -verify -pubin -inkey "$keyfile" -rawin \
        -in "$data" -sigfile "$sig" >/dev/null 2>&1; then
      status=0
      break
    fi
  done
  return "$status"
}

# ── Detect platform ─────────────────────────────────────────────────

detect_os() {
  os="$(uname -s)"
  case "$os" in
    Linux*)   echo "Linux" ;;
    Darwin*)  echo "Darwin" ;;
    FreeBSD*) echo "Freebsd" ;;
    MINGW*|MSYS*|CYGWIN*) echo "Windows" ;;
    *) err "Unsupported OS: $os" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)   echo "x86_64" ;;
    aarch64|arm64)   echo "arm64" ;;
    armv7*|armv6*)   echo "armv${arch#armv}" ;;
    riscv64)         echo "riscv64" ;;
    s390x)           echo "s390x" ;;
    mips64*)         echo "mips64" ;;
    *) err "Unsupported architecture: $arch" ;;
  esac
}

# ── Resolve latest version ──────────────────────────────────────────

latest_version() {
  resp=$(http_get_stdout "https://api.github.com/repos/${REPO}/releases/latest")
  tag=$(printf '%s' "$resp" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
  [ -n "$tag" ] || err "Could not determine latest release."
  echo "$tag"
}

# ── Parse arguments ─────────────────────────────────────────────────

VERSION=""
PREFIX=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version|-v)   VERSION="$2"; shift 2 ;;
    --prefix|-p)    PREFIX="$2"; shift 2 ;;
    --help|-h)
      cat <<EOF
Lele Installer

Options:
  --version, -v   Install a specific version (e.g. v0.2.0)
  --prefix,  -p   Installation prefix (default: ~/.local)
  --help,    -h   Show this help

The binary is placed in <prefix>/bin/lele.
EOF
      exit 0
      ;;
    *) err "Unknown option: $1" ;;
  esac
done

PREFIX="${PREFIX:-$DEFAULT_PREFIX}"
BIN_DIR="${PREFIX}/bin"

# ── Main ─────────────────────────────────────────────────────────────

OS=$(detect_os)
ARCH=$(detect_arch)

info "Detected platform: ${OS}/${ARCH}"

HASH_TOOL=$(detect_hash_tool)

if [ -z "$VERSION" ]; then
  info "Fetching latest release..."
  VERSION=$(latest_version)
fi

# Strip leading 'v' for archive name
VERSION_NUM="${VERSION#v}"

# Build download URL
if [ "$OS" = "Windows" ]; then
  ARCHIVE="${BINARY}_${OS}_${ARCH}.zip"
else
  ARCHIVE="${BINARY}_${OS}_${ARCH}.tar.gz"
fi

URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# Authenticity (ed25519 signature over the checksums file) and integrity
# (sha256) are established before the archive is downloaded and long before it
# is extracted: nothing derived from the release is trusted first.
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${VERSION_NUM}_checksums.txt"
SIGNATURE_URL="${CHECKSUMS_URL}.sig"

info "Verifying release authenticity..."
http_get "$CHECKSUMS_URL" "${TMPDIR}/checksums.txt"

signature_status=0
fetch_signature "$SIGNATURE_URL" "${TMPDIR}/checksums.txt.sig" || signature_status=$?

case "$signature_status" in
  0)
    if ! command -v openssl >/dev/null 2>&1; then
      log "Warning: openssl not found, skipping signature verification."
      log "The sha256 checksum is still enforced; install OpenSSL >= 3.0 for the full check."
    elif ! openssl_can_verify_rawin; then
      log "Warning: this openssl cannot verify ed25519 signatures."
      log "Skipping signature verification; OpenSSL >= 3.0 is required for it."
      log "The sha256 checksum is still enforced."
    elif verify_pinned_signature "${TMPDIR}/checksums.txt" "${TMPDIR}/checksums.txt.sig"; then
      log "Release signature OK"
    else
      err "Release signature verification failed. The download may have been tampered with."
    fi
    ;;
  2)
    log "Warning: this release is not signed, skipping signature verification."
    ;;
  *)
    err "Could not download the release signature (network or server error).
Retry when the connection is healthy; authenticity is never skipped on a transport failure."
    ;;
esac

info "Downloading ${BINARY} ${VERSION} ..."
log "$URL"
http_get "$URL" "${TMPDIR}/${ARCHIVE}"

info "Verifying checksum..."

EXPECTED=$(grep "${ARCHIVE}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
  err "Archive '${ARCHIVE}' not found in checksums file."
fi

ACTUAL=$(hash_file "${TMPDIR}/${ARCHIVE}")

if [ "$ACTUAL" != "$EXPECTED" ]; then
  err "Checksum mismatch!\n  Expected: ${EXPECTED}\n  Got:      ${ACTUAL}"
fi
log "Checksum OK"

# Extract
info "Extracting..."
if [ "$OS" = "Windows" ]; then
  need unzip
  unzip -qo "${TMPDIR}/${ARCHIVE}" -d "${TMPDIR}/out"
else
  need tar
  mkdir -p "${TMPDIR}/out"
  tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}/out"
fi

# Install
mkdir -p "$BIN_DIR"

if [ "$OS" = "Windows" ]; then
  EXTRACTED="${TMPDIR}/out/${BINARY}.exe"
else
  EXTRACTED="${TMPDIR}/out/${BINARY}"
fi

if [ ! -f "$EXTRACTED" ]; then
  err "Binary not found in archive. Contents: $(ls "${TMPDIR}/out/")"
fi

cp "$EXTRACTED" "${BIN_DIR}/${BINARY}"
chmod +x "${BIN_DIR}/${BINARY}"

ok "Installed ${BINARY} ${VERSION} to ${BIN_DIR}/${BINARY}"

# Check PATH
case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *)
    log ""
    log "Add the following to your shell profile to put lele on your PATH:"
    log ""
    log "  export PATH=\"${BIN_DIR}:\$PATH\""
    log ""
    ;;
esac

log "Run 'lele --help' to get started."
