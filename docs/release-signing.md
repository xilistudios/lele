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
| Signer/verifier CLI | `scripts/sign-checksums` (`keygen`, `sign`, `verify`) |
| Release wiring | `.goreleaser.yaml` (`checksum` + `signs`) |
| Signing secret | GitHub Actions secret `LELE_RELEASE_SIGNING_KEY` (release workflow, `release` job env) |
| Pinned public key (Go) | `pkg/update/release_signing_key.go`, const `ReleaseSigningPublicKeyBase64` |
| Pinned public key (shell) | `install.sh`, `RELEASE_SIGNING_PUBKEY_PEM` |
| Offline private key backup | maintainer workstation, mode `0600`, outside every repository (`~/state/lele-release-signing-key.ed25519.pem`) |

Both pinned copies are pinned **on purpose**: `pkg/update` cannot shell out to
`openssl`, and `install.sh` cannot call into Go. `TestInstallSh_PinnedKeyMatchesPackage`
(`pkg/update/install_sh_test.go`) fails if the two copies ever diverge.

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

### `install.sh`

The installer is fail-closed on integrity and best-effort on authenticity:

- **Checksum: mandatory.** `sha256sum` or `shasum` must exist; without one the
  installer exits non-zero (`sha256sum or shasum is required to verify the
  download; install one and retry.`). It never installs an unverified archive.
- **Signature: verified when `openssl` is present.** The installer writes the
  pinned public key to a temporary PEM and runs
  `openssl pkeyutl -verify -pubin -inkey <pem> -rawin -in checksums.txt -sigfile checksums.txt.sig`.
  A signature that is present but does **not** verify is fatal
  (`Release signature verification failed. The download may have been tampered with.`).
- **`openssl` missing** → a loud warning, then the checksum-only path (HTTPS +
  SHA-256). `openssl` is not made mandatory so a minimal container can still
  install.
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

Because the public key is pinned inside the binary, rotation is a two-step
process — a client verifies the **next** release with the key baked into the
**running** binary:

1. Generate the new pair (K2). Change **nothing** about signing yet: the current
   secret is still K1.
2. Ship a release signed with **K1** whose source pins **K2** in
   `pkg/update/release_signing_key.go` and `install.sh`. Clients update to it
   happily because they still trust K1.
3. After that release has been adopted, replace the `LELE_RELEASE_SIGNING_KEY`
   secret with **K2** and sign every later release with K2. Clients running step
   2 or later accept them.
4. Retire K1 (destroy the backup) once you no longer need to sign releases for
   step-1 clients. Clients that skipped step 2 cannot auto-update across the
   rotation and must reinstall from `install.sh` (which always carries the newest
   pinned key).

**Compromise**: skip step 3's patience. Generate K3, pin it, sign with K3
immediately, destroy the compromised backup, and accept that clients older than
the release pinning K3 fail closed and need a manual reinstall. Failing closed is
the point.

## Verifying a release by hand

```sh
gh release download v0.7.10 --pattern 'lele_0.7.10_checksums.txt*'
go run ./scripts/sign-checksums verify \
  --key-env LELE_RELEASE_SIGNING_PUBKEY \
  lele_0.7.10_checksums.txt lele_0.7.10_checksums.txt.sig
sha256sum --check --ignore-missing lele_0.7.10_checksums.txt
```

or, without Go:

```sh
openssl pkeyutl -verify -pubin -inkey release-signing-key.pem \
  -rawin -in lele_0.7.10_checksums.txt -sigfile lele_0.7.10_checksums.txt.sig
```

where `release-signing-key.pem` is the PEM block pinned in `install.sh`.

## Local and CI notes

- Signing needs `LELE_RELEASE_SIGNING_KEY` in the environment. Snapshot/local
  releases do not have it: run `goreleaser release --snapshot --clean --skip=sign`
  (or export a throwaway key generated with `keygen`).
- The release workflow is manual-dispatch; when the secret is missing the release
  fails at the signing step instead of publishing an unsigned release.
- `goreleaser check` validates the `checksum`/`signs` blocks.
- Unit tests never use the production key: `pkg/update` and
  `scripts/sign-checksums` generate a throwaway key pair per test, and the
  installer harness rewrites the pinned PEM in a temporary copy of `install.sh`.