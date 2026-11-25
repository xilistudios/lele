# Release signing

lele releases are signed with a single **ed25519** key. One detached signature is
published per release, over the SHA-256 checksums file, and every verification
path in the project checks that signature **before** it trusts any checksum.

## Why

Before this scheme the only integrity check was a SHA-256 hash of the archive
published in the *same* GitHub release as the archive itself. Whoever can publish
a release can publish a matching hash, so a compromised release account (or a
compromised repository) could replace artifact **and** checksum and every
installation that auto-updates would install the attacker's binary. A signature
made with a key that never touches the repository closes that gap: the private
key lives in a GitHub Actions secret and in an offline backup, the public key is
pinned inside the binary and inside `install.sh`.

## What is signed

| Asset | Contents |
|---|---|
| `lele_<version>_checksums.txt` | `sha256  filename` for every archive of the release (produced by GoReleaser) |
| `lele_<version>_checksums.txt.sig` | raw 64-byte ed25519 signature over the exact bytes of the checksums file |

Signing the checksums file (instead of each archive) keeps the number of
signatures constant: one signature binds every archive, and `pkg/update` verifies
the archive against the signed checksums afterwards.

The signature is **detached and raw** — no PEM envelope, no ASN.1, no digest
step (ed25519 hashes internally).

## Where the pieces live

| Piece | Location |
|---|---|
| Signer/verifier CLI | `scripts/sign-checksums` (`keygen`, `derive-pub`, `sign`, `verify`) |
| Release wiring | `.goreleaser.yaml` (`checksum` + `signs`) |
| Signing secret | GitHub Actions secret `LELE_RELEASE_SIGNING_KEY` (release workflow, `release` job env) |
| Pinned public key (Go) | `pkg/update/release_signing_key.go`, const `ReleaseSigningPublicKeyBase64` |
| Pinned public key (shell) | `install.sh`, `RELEASE_SIGNING_PUBKEYS_PEM` (one or more PEM blocks) |
| Offline private key backup | maintainer workstation, mode `0600`, outside every repository (`~/state/lele-release-signing-key.ed25519.pem`) |

Both pinned copies are pinned **on purpose**: `pkg/update` cannot shell out to
`openssl`, and `install.sh` cannot call into Go. `TestInstallSh_PinnedKeyMatchesPackage`
(`pkg/update/install_sh_test.go`) fails if the Go pin is not one of the keys pinned
in `install.sh`.

The two pins have different shapes for a reason. The Go pin is **one** key: it is
compiled into the binary, so it can only change by shipping a release. The shell
pin is a **list** of PEM blocks (currently one): `install.sh` is served from
`main` and used immediately, so it must be able to accept a release signed by
either the current or the previous key while a rotation is in flight. Verification
succeeds when **any** pinned key verifies the signature; the first block is the
current key and is the one that must match the Go pin.

## Verification paths

### `pkg/update` (self-update: TUI `/update`, `lele update`, auto-update)

`Downloader.Download` resolves three assets and fails closed at the first one
that is missing:

1. `lele_<ver>_checksums.txt` — downloaded first.
2. `lele_<ver>_checksums.txt.sig` — downloaded next. **A release without this
   asset is rejected**: `release <tag> is not signed: no <asset> asset (refusing
   to install an unsigned release)`.
3. `ed25519.Verify(pinnedKey, checksumsBytes, signature)` over the exact file
   bytes. A bad signature aborts with `invalid release signature`; a signature
   that is not 64 bytes aborts with the same error plus the size it saw.

Only then is the archive downloaded and its SHA-256 compared with the (now
trusted) checksums file, and only then is it extracted and swapped in. Order
matters: nothing derived from the release is trusted before the signature is
verified.

The pinned key is decoded once at package init. If the constant is ever corrupt
the decoded key is `nil` and every update is rejected with
`release signing key is not configured` — a broken pin fails closed, never open.
`Download` also refuses to run at all when its key is unset, so the "no key
configured" state is reachable through the downloader itself, not only through
the verifier.

The two pre-authentication downloads are capped (checksums 1 MiB, signature
1 KiB) with `io.LimitReader`, so a hostile release cannot fill the disk or OOM
the verifier with an oversized `.sig` or checksums file *before* the signature
is checked.

### `install.sh`

The installer establishes authenticity and integrity **before** it downloads the
archive (same order as `pkg/update`), and it is fail-closed on integrity,
best-effort on authenticity:

1. `lele_<ver>_checksums.txt` is downloaded.
2. `lele_<ver>_checksums.txt.sig` is downloaded, with the request outcome
   classified: a **404/410** means the release predates signing (warning, keep
   going); a **transport failure** (DNS, connection, TLS, timeout, server error)
   is retried twice and then fatal — `Could not download the release signature
   (network or server error).` Authenticity is never silently downgraded because
   the network flaked.
3. The signature is verified against each pinned key in turn.
4. Only then is the archive downloaded, its SHA-256 compared with the now-trusted
   checksums file, and the binary extracted.

- **Checksum: mandatory.** `sha256sum` or `shasum` must exist; without one the
  installer exits non-zero before downloading anything
  (`sha256sum or shasum is required to verify the download; install one and
  retry.`). It never installs an unverified archive.
- **Signature: requires OpenSSL ≥ 3.0.** Verification uses
  `openssl pkeyutl -verify -pubin -inkey <pem> -rawin -in checksums.txt -sigfile checksums.txt.sig`,
  and `-rawin` (oneshot ed25519) only exists in **OpenSSL ≥ 3.0**. The installer
  therefore probes the capability first
  (`openssl pkeyutl -help | grep -q -- -rawin`) instead of trusting the exit
  status: an openssl that merely *cannot do the operation* fails on **every**
  signature, valid or forged, so its verdict is never read as "tampered".
  - **capable openssl + signature does not verify** → fatal:
    `Release signature verification failed. The download may have been tampered with.`
  - **incapable openssl** (older OpenSSL, LibreSSL, BoringSSL — e.g. macOS
    `/usr/bin/openssl`, Debian 11, Ubuntu 20.04, RHEL 8, FreeBSD base) → a loud
    warning and the checksum-only path. The same happens when `openssl` is
    missing entirely. `openssl` is not made mandatory so a minimal container can
    still install, and the authenticated path for those hosts is
    `lele update` (which uses the pinned key, no openssl needed).
- **No signature published** (releases published before this scheme) → warning,
  install proceeds. This is what makes the transition below work.

`install.ps1` (Windows) is out of scope of this change: it verifies nothing today
and is tracked separately.

## Transition semantics

- **Old client → new release**: unaffected. Binaries built before this change
  never look for `.sig`; they keep verifying the checksum exactly as before.
  `install.sh` as published previously behaves the same way.
- **New client → unsigned release**: rejected. A binary built with this change
  refuses any release that has no `.sig` asset, including releases published
  before the scheme existed. That is intentional (forward-only verification) and
  it means the **first release that ships this code must itself be signed**,
  together with every later one.
- **Pinning to an old tag** (`lele update --version v0.7.9`) is therefore
  rejected by a new client with `is not signed`. Downgrades have never been
  supported by the update path; the failure mode is now explicit instead of
  silently downgrading to an unverifiable artifact.
- **Recovery**: a client that must move to a pre-signing release has to reinstall
  it by hand (`install.sh --version <tag>`, which accepts unsigned releases with
  a warning).

## Key management

The private key is a 64-byte ed25519 private key, base64-encoded, and is used in
exactly two places:

1. **GitHub Actions secret** `LELE_RELEASE_SIGNING_KEY` in `xilistudios/lele`.
   `release.yml` exports it into the GoReleaser step; GoReleaser runs
   `go run ./scripts/sign-checksums sign --key-env LELE_RELEASE_SIGNING_KEY ...`.
   A **preflight step** in the same job derives the public key from the secret
   (`go run ./scripts/sign-checksums derive-pub`) and compares it with the pinned
   constant **before** GoReleaser runs, so a secret that does not match the pin
   fails the release instead of publishing artifacts nobody can verify.
2. **Offline backup**, mode `0600`, outside every repository
   (`~/state/lele-release-signing-key.ed25519.pem` on the maintainer
   workstation). The file holds the base64 secret value — the exact bytes to
   restore into the secret — and nothing else.

Rules that keep the key safe:

- The key is read from an **environment variable**, never from argv — argv is
  visible in `/proc/<pid>/cmdline` and in CI logs. `sign` refuses to run when the
  variable is empty.
- The key never enters the repository, a commit, a PR body, an issue or a log.
  `keygen` writes the key pair to stdout, so it is always redirected into a
  `0600` file (generate under `umask 077`) and shredded afterwards.
- Backups live outside the repo tree. Losing the key is recoverable
  (`scripts/sign-checksums keygen` + rotate); leaking it is not — rotate
  immediately (see below).

**Known residual (environment inheritance).** The secret is injected into the
GoReleaser step's environment, so it is also inherited by everything that step
spawns: the `before.hooks` (`bun run build`, `go generate ./cmd/lele`), every
`go build`, and `go run ./scripts/sign-checksums`. That code is repository code
running in a job that already holds `contents: write`, so the exposure is the
pipeline itself, not an extra trust boundary — but the precise statement is "the
key is in the environment of everything the release step runs", not "the key is
only in the signer". The narrow fix, if it ever matters, is a separate signing
step that downloads `dist/*checksums.txt` and signs it with nothing else in
reach; it is not done here because GoReleaser v2's `signs[].env` cannot narrow
an inherited environment.

### Generating a new key pair

```sh
umask 077
cd <repo>
go run ./scripts/sign-checksums keygen > /tmp/lele-keygen.txt
# /tmp/lele-keygen.txt now holds, one per line:
#   PRIVATE_KEY_BASE64=...      64-byte private key (the shape of the secret)
#   PUBLIC_KEY_BASE64=...       32-byte public key (pin in pkg/update)
#   PUBLIC_KEY_PEM_BASE64=...   the same key as PKIX PEM (pin in install.sh)
```

Then:

```sh
GH_TOKEN=<token with admin:repo> gh secret set LELE_RELEASE_SIGNING_KEY \
  --repo xilistudios/lele < /tmp/lele-keygen.txt-filtered-to-the-private-line
gh secret list --repo xilistudios/lele   # confirms the secret exists
```

and copy `PUBLIC_KEY_BASE64` into `ReleaseSigningPublicKeyBase64` and the PEM
body into `RELEASE_SIGNING_PUBKEY_PEM`, run `go test ./pkg/update/`, then shred
the temporary file.

### Rotation

Because the public key is pinned inside the binary, rotation is a staged process:
a client always verifies the **next** release with the key baked into the
**running** binary. `install.sh` is different — it is served from `main` and runs
immediately — so during the transition it must accept **both** keys. That is what
the key list is for: pin K2 **next to** K1, never instead of it.

1. Generate the new pair (K2). Change **nothing** about signing yet: the current
   secret is still K1.
2. Ship a release signed with **K1** whose source pins **K2**:
   `pkg/update/release_signing_key.go` switches to K2 (the Go pin is single and
   forward-only), and `install.sh` carries **K1 and K2** in
   `RELEASE_SIGNING_PUBKEYS_PEM`. Clients update to it happily because they still
   trust K1, and every `curl … | sh` keeps working because the list still accepts
   a K1-signed release (that is the current release at this point).
3. After that release has been adopted, replace the `LELE_RELEASE_SIGNING_KEY`
   secret with **K2** and sign every later release with K2. Clients running step
   2 or later accept them, and so does `install.sh` (K2 is already in the list).
4. Retire K1: drop its PEM block from `install.sh` (leaving K2 only) **after** the
   first K2-signed release is out, then destroy the K1 backup once you no longer
   need to sign releases for step-1 clients. Clients that skipped step 2 cannot
   auto-update across the rotation and must reinstall from `install.sh`
   (`install.sh --version <tag>` works for any tag whose signature matches a key
   still in the list).

The overlap window is therefore "from the release that pins both keys until the
release that drops the old one", and `install.sh` installs correctly at every
point in it. `TestInstallSh_VerifiesAgainstSecondPinnedKey` and
`TestInstallSh_RejectsSignatureWhenNoPinnedKeyMatches` pin both halves of that
contract.

**Compromise**: skip step 3's patience. Generate K3, pin it in
`pkg/update/release_signing_key.go` **and** in `install.sh` (replacing the
compromised key there — no overlap, the old key must not verify anything),
sign with K3 immediately, destroy the compromised backup, and accept that clients
older than the release pinning K3 fail closed and need a manual reinstall.
Failing closed is the point. Note that such a release **cannot arrive by
auto-update** for step-1 clients; it has to be installed by hand.

## Verifying a release by hand

```sh
gh release download v0.7.10 --pattern 'lele_0.7.10_checksums.txt*'
go run ./scripts/sign-checksums verify \
  --key-env LELE_RELEASE_SIGNING_PUBKEY \
  lele_0.7.10_checksums.txt lele_0.7.10_checksums.txt.sig
sha256sum --check --ignore-missing lele_0.7.10_checksums.txt
```

or, without Go (needs OpenSSL ≥ 3.0 — see the `install.sh` requirement above):

```sh
openssl pkeyutl -verify -pubin -inkey release-signing-key.pem \
  -rawin -in lele_0.7.10_checksums.txt -sigfile lele_0.7.10_checksums.txt.sig
```

where `release-signing-key.pem` is a PEM block pinned in `install.sh`.

### Confirming that the secret and the pin agree

A release is only installable if the **secret** used to sign it and the **public
key** pinned in the tree are a pair. Nothing in the client can check that — it is
a property of the repository's configuration — so the release workflow checks it
before signing, and you can check it by hand:

```sh
# derived from the Actions secret, never from the pinned constant
LELE_RELEASE_SIGNING_KEY=<the secret> go run ./scripts/sign-checksums derive-pub \
  --key-env LELE_RELEASE_SIGNING_KEY
# -> the base64 public key, which must equal ReleaseSigningPublicKeyBase64
sed -n 's/.*ReleaseSigningPublicKeyBase64 = "\([^"]*\)".*/\1/p' \
  pkg/update/release_signing_key.go
```

`derive-pub` prints **only** the public key; it never echoes the private one. The
same comparison runs as the "Verify the signing secret matches the pinned public
key" step of the release job, so a rotation that swaps the secret but not the pin
(or the pin but not the secret) fails the release instead of silently shipping
unverifiable artifacts.

## Local and CI notes

- Signing needs `LELE_RELEASE_SIGNING_KEY` in the environment. Snapshot/local
  releases do not have it: run `goreleaser release --snapshot --clean --skip=sign`
  (or export a throwaway key generated with `keygen`).
- The release workflow is manual-dispatch; when the secret is missing the release
  fails at the signing step instead of publishing an unsigned release.
- `goreleaser check` validates the `checksum`/`signs` blocks.
- Unit tests never use the production key: `pkg/update` and
  `scripts/sign-checksums` generate a throwaway key pair per test, and the
  installer harness rewrites the pinned PEM list in a temporary copy of
  `install.sh`.
- The Go filename contract is pinned to the release config: `TestChecksumsNameMatchesGoreleaserConfig`
  and `TestChecksumsSignatureNameMatchesGoreleaserConfig` render the `checksum.name_template`
  and `signs[].signature` values from `.goreleaser.yaml` and compare them with
  `ChecksumsName` / `ChecksumsSignatureName`, so a template edit that renames the
  published assets fails the test suite instead of bricking auto-update.