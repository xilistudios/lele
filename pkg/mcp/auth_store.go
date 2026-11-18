package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// This file is the T4 auth store: the on-disk home of ONE lele directory's
// OAuth 2.1 state for REMOTE MCP servers (mcp-auth.json). It is deliberately
// self-contained, and three rules shape every line below.
//
//  1. BOUNDARY. pkg/mcp imports only pkg/logger and pkg/tools (one-way);
//     pkg/tools never imports pkg/mcp. Nothing here reaches for pkg/config,
//     pkg/agent, pkg/auth, pkg/keyring or pkg/store: the caller (the CLI, the
//     TUI, the gateway) knows where the lele directory is and HANDS IT IN —
//     the store is built from a directory string, so this package stays
//     unit-testable with t.TempDir() and no agent machinery at all.
//
//     The secret VAULT crosses that boundary as an interface only. Callers that
//     have one (cmd/lele wires a keyring-backed adapter) either register a
//     factory with RegisterSecretVaultFactory once at startup or inject one
//     explicitly with WithSecretVault; pkg/mcp then talks to a SecretVault and
//     never to its implementation. See secret_vault.go.
//
//  2. SECRECY. Access tokens, refresh tokens and client secrets are values that
//     never leave pkg/mcp: nothing in this file logs any field of the file, and
//     error strings name the PATH (or the server NAME, a mcp.json key) only.
//     Callers ask yes/no questions (Authenticated, AuthRequired) or get the
//     value handed straight to the mcp-go transport through TokenStore — never
//     a string to display, print or JSON-echo to a surface. The client ID is
//     the one identifier that stays in the file: it is the public half of a
//     dynamic registration.
//
//     With a vault the file holds NO credential at all: client_secret and the
//     token move to "mcp.<server>.auth" (see authSecretBlob), and every read
//     overlays them back onto the file-backed record. Writes fail CLOSED — a
//     vault error is returned, never silently downgraded to plaintext — while
//     reads DEGRADE TO ABSENT (a vault that cannot be read is a warning plus a
//     record without secrets, which leads to a re-login and never to a wrong
//     credential being presented). Reads take the vault as the LIVE source for
//     secrets: the file's stat stamp only covers non-secret state, because the
//     other lele process may refresh a token in the vault and never touch this
//     file. That is also why Revision hashes the MERGED record.
//
//  3. SCOPE. mcp-auth.json is per-lele-directory and machine-local: it stores
//     tokens a specific machine obtained from a specific authorization
//     server, so it is not a config file to be synced or shared. It sits next
//     to mcp.json because it is that file's other half: mcp.json says WHAT to
//     connect to, mcp-auth.json remembers what the server asked us to prove.
//
// Freshness is the fourth concern and it is CROSS-PROCESS: two lele processes
// (the TUI and the gateway, say) share the same lele directory, and each has
// its own AuthStore. Every read therefore revalidates the file with a cheap
// os.Stat and reloads when mtime/size changed or when the file appeared or
// disappeared, so a login performed in one process is visible in the other.
// One sync.Mutex guards the cache and serialises the writers.
//
// With a vault there are TWO freshness sources and they cover different halves:
// the stat stamp covers the non-secret state in the file, and the vault is read
// LIVE on every call for the secret half — never cached in the servers map. A
// token refreshed by another process is therefore visible immediately even
// though mcp-auth.json was not rewritten (and its mtime did not move).
//
// MIGRATION is eager and happens once, in NewAuthStore: when a vault is active,
// any credentials still in the file are pushed into the vault and the file is
// rewritten once without them. A record whose SetSecret fails keeps its secrets
// in the file (no data loss) and, for that record, the FILE WINS over the vault
// on reads until a later Save moves them — so an interrupted or partially
// failed migration is always readable rather than half-lost.

// authFileName is the file NewAuthStore owns inside the lele directory.
const authFileName = "mcp-auth.json"

// AuthRecord is one server's persisted OAuth state. It is keyed by the server
// NAME in mcp-auth.json; URL ties the record to the resource it was obtained
// for, so a record left over from an edited entry is treated as absent.
type AuthRecord struct {
	URL                 string           `json:"url"`
	ClientID            string           `json:"client_id,omitempty"`
	ClientSecret        string           `json:"client_secret,omitempty"`
	ResourceMetadataURL string           `json:"resource_metadata_url,omitempty"`
	RedirectURI         string           `json:"redirect_uri,omitempty"`
	AuthRequired        bool             `json:"auth_required,omitempty"`
	Token               *transport.Token `json:"token,omitempty"`
}

// authEnvelope is the file's top-level shape: {"servers": {...}}. The object
// is keyed by server NAME for the same reason mcp.json is — the name is the
// only stable identity a record has across an edit that rewrites the file —
// and the extra envelope leaves room for a future "version" member without
// touching the record shape.
type authEnvelope struct {
	Servers map[string]AuthRecord `json:"servers"`
}

// AuthStore is the file-backed AuthStore for one lele directory. The zero
// value is NOT usable: build it with NewAuthStore so the path is set.
type AuthStore struct {
	mu   sync.Mutex
	path string // "" = disabled (NewAuthStore("")): reads absent, writes no-ops

	// vault, when non-nil, is where the CREDENTIAL half of every record lives
	// (client_secret and the token). It is resolved once, in NewAuthStore, and
	// never replaced afterwards: the pointer is read without the mutex, so it
	// must not change. nil = legacy mode, the file holds everything.
	vault SecretVault

	// Cached load, guarded by mu. The stamp is what makes a read cheap: a
	// stat that reports the same existence, mtime and size as the cached load
	// means the bytes on disk are the bytes we already hold — including when
	// the OTHER lele process wrote them.
	loaded  bool
	existed bool
	modTime time.Time
	size    int64

	servers map[string]AuthRecord
}

// AuthStoreOption customises a store built by NewAuthStore. Options exist so
// the constructor stays backward compatible (every pre-vault call site passes
// just a directory) while a caller — usually a test — can inject a vault
// explicitly instead of registering a process-wide factory.
type AuthStoreOption func(*AuthStore)

// WithSecretVault injects v as the store's vault, overriding whatever the
// registered factory would return. A nil v means "no vault" and does NOT fall
// back to the factory: the option is the caller's explicit statement of what
// this store uses.
func WithSecretVault(v SecretVault) AuthStoreOption {
	return func(s *AuthStore) { s.vault = v }
}

// NewAuthStore returns the store backed by <leleDir>/mcp-auth.json. An empty
// leleDir yields a DISABLED store: every read reports "absent" and every write
// is a no-op (never create a process-relative file).
//
// Disabled is a real mode, not a degenerate one: a caller that could not
// resolve a lele directory (or explicitly wants OAuth state kept off disk)
// must still be able to hand a TokenStore to the transport and get a clean
// "no token" answer instead of a panic or a stray ./mcp-auth.json next to
// whatever working directory the binary happened to start in.
//
// The SECRET VAULT (see secret_vault.go) is resolved here, once:
//
//  1. an explicit WithSecretVault option wins;
//  2. otherwise the factory registered with RegisterSecretVaultFactory is
//     consulted with leleDir;
//  3. otherwise there is none.
//
// A disabled store never gets a vault. When a vault IS resolved the constructor
// performs the one-time legacy migration described in this file's header: any
// client_secret/token still sitting in mcp-auth.json moves into the vault and
// the file is rewritten without them. Migration is best-effort by design — it
// cannot fail the constructor — but it never loses a credential: a record it
// could not store stays in the file, where it keeps being read.
func NewAuthStore(leleDir string, opts ...AuthStoreOption) *AuthStore {
	s := &AuthStore{}
	if leleDir != "" {
		s.path = filepath.Join(leleDir, authFileName)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	// Resolve the vault AFTER the options (an explicit injection wins) and
	// only for an enabled store: a disabled store has nowhere to persist
	// non-secret state either, so handing it a vault would let it write secrets
	// for a server it cannot record.
	if s.path == "" {
		s.vault = nil
	} else if s.vault == nil {
		s.vault = resolveSecretVault(leleDir)
	}
	if s.vault != nil {
		s.migrateLegacySecrets()
	}
	return s
}

// Enabled reports whether the store is backed by a file. A disabled store
// (NewAuthStore("")) reads as empty and discards every write, so an
// interactive flow — which must persist its registration — has to refuse to
// start without one.
func (s *AuthStore) Enabled() bool {
	return s != nil && s.path != ""
}

// Record returns the stored record for name, with the vault's secrets merged
// back in when a vault is active. A record whose URL does not match the one
// recorded is NOT special-cased here (callers pass the URL they want to match
// via Authenticated/AuthRequired/TokenStore); Record is the raw accessor.
//
// The returned record is a copy, token included: a caller may edit what it
// holds without corrupting the cache that other goroutines read.
func (s *AuthStore) Record(name string) (AuthRecord, bool) {
	return s.resolveRecord(name)
}

// resolveRecord is the single read path: refresh the file-backed cache, copy
// the record, then overlay the LIVE vault secrets on top.
//
// The mutex is released before the vault is consulted on purpose. The vault is
// external I/O (a keyring lookup, a file read), and holding the store lock
// across it would serialise every reader of every server behind one keyring
// call. Nothing the vault returns is cached, so no lock is needed to read it.
func (s *AuthStore) resolveRecord(name string) (AuthRecord, bool) {
	if s == nil || s.path == "" {
		return AuthRecord{}, false
	}
	s.mu.Lock()
	s.refreshLocked()
	rec, ok := s.servers[name]
	s.mu.Unlock()
	if !ok {
		return AuthRecord{}, false
	}
	return s.mergeSecrets(name, rec), true
}

// mergeSecrets returns rec (a file-backed record) with the vault's secrets laid
// on top. rec is copied first, so neither the cached map nor any record another
// goroutine handed out is ever mutated.
//
// PRECEDENCE, and it is the reason the "file carries a secret" branch exists:
// a file record that still holds client_secret or a token has not been migrated
// (a failed SetSecret, a hand-edited file, a downgrade to an older lele). For
// that record the FILE WINS and the vault is not consulted at all — mixing the
// halves of two different credentials would present a client secret from one
// registration with a token from another, and the safe reading of "the file
// still owns this credential" is to keep it whole. The next successful Save
// moves it into the vault and this branch stops being taken.
//
// A vault READ error and a malformed blob are treated identically: warn (value
// free) and report no secrets. Record cannot return an error, and "no secrets"
// degrades to a re-login, whereas surfacing a half-read credential could
// present a token whose client secret we just failed to load.
func (s *AuthStore) mergeSecrets(name string, rec AuthRecord) AuthRecord {
	rec = copyRecord(rec)
	if s.vault == nil {
		return rec
	}
	if rec.ClientSecret != "" || rec.Token != nil {
		return rec // unmigrated: the file still owns this credential whole
	}
	raw, err := s.vault.GetSecret(secretNameFor(name))
	if err != nil {
		logWarnValueFree("stored OAuth secret could not be read; treating the server as not authenticated", name, err)
		return rec
	}
	blob, ok := parseAuthSecretBlob(name, raw)
	if !ok {
		return rec
	}
	rec.ClientSecret = blob.ClientSecret
	rec.Token = authTokenCopy(blob.Token)
	return rec
}

// Authenticated reports whether name holds a USABLE token for url: a token
// exists and either is not expired or carries a refresh token (mcp-go refreshes
// it on the next request).
//
// The predicate answers the question a surface actually asks — "will the next
// request go through without a login?" — so it is the mirror of mcp-go's
// OAuthHandler.getValidToken rather than a bare "a token is stored" test: an
// expired token WITH a refresh token is usable (the transport refreshes it),
// an expired token WITHOUT one is not. A record bound to another url is
// absent: after a user edits an entry's URL the old token belongs to a
// different resource and presenting it would be a protocol error, so the
// safest reading of a stale record is "no token".
func (s *AuthStore) Authenticated(name, url string) bool {
	rec, ok := s.recordFor(name, url)
	return ok && usableToken(rec.Token)
}

// AuthRequired reports whether name is known to require authorization: an
// explicit flag recorded after a real 401 was observed. Never inferred.
//
// The flag is a hint for surfaces ("this server will need a login"), not a
// discovery result: lele only sets it after a server actually answered 401,
// so a server that merely LOOKS private stays unflagged and the UI does not
// nag about a login that may never be needed. Like Authenticated it is bound
// to url, and it survives a token write (see authTokenStore.SaveToken).
func (s *AuthStore) AuthRequired(name, url string) bool {
	rec, ok := s.recordFor(name, url)
	return ok && rec.AuthRequired
}

// Revision is a stable FNV-1a hash of the record's JSON bytes, 0 when absent.
// It is what makes a token write invalidate the manager's cached connection.
//
// Comparing revisions is how the connection manager answers "is the token I
// dialled with still the token on disk?" without ever holding the value: the
// hash of the marshalled record changes when ANY field changes, is stable
// across repeated reads (marshalling the same bytes twice gives the same
// hash), and maps every absent record to 0 so "gone" is one constant.
//
// The hash covers the MERGED record (file state + live vault overlay), not just
// the file. That is what keeps the manager honest when a vault is active: a
// token refreshed by ANOTHER process changes only the vault entry, leaving
// mcp-auth.json — and therefore its mtime, its size and the stat stamp — exactly
// as it was. Hashing the file alone would report "nothing changed" and keep
// dialling a retired token.
//
// The cost is one vault read per call, and the manager calls this on every
// resolve and every load; the vault is the freshness source for secrets, so that
// read is the price of correctness (a keyring lookup, not a network round trip).
func (s *AuthStore) Revision(name string) uint64 {
	rec, ok := s.resolveRecord(name)
	if !ok {
		return 0
	}
	return authRevision(rec)
}

// Save writes record for name atomically (0600). Disabled store: no-op.
//
// Save is the RAW writer: it replaces the whole record, so a caller that
// means to keep a registration (client_id/client_secret/resource metadata)
// must read the current record first and hand it back — which is exactly what
// authTokenStore does. The write reuses SaveRawFile (temp file in the same
// directory → fsync → chmod 0600 → rename), so a crash never leaves a
// truncated or world-readable mcp-auth.json: the 0600 mode is deliberate,
// because the file holds a client id and resource URLs even when the vault owns
// the credentials (and holds the credentials themselves when it does not).
//
// With a vault the record is SPLIT: client_secret and the token go to
// "mcp.<server>.auth" (an empty pair DELETES that entry instead of storing an
// empty object) and the file keeps only the non-secret fields. The vault is
// written FIRST and its error is returned unchanged: writes fail CLOSED, so a
// broken vault can never silently downgrade the user to plaintext on disk. A
// failure of the file write AFTER a successful vault write is also returned,
// and the state stays consistent anyway — the next read overlays the vault on
// top of whatever the file says.
func (s *AuthStore) Save(name string, rec AuthRecord) error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()

	stored := rec
	if s.vault != nil {
		if err := s.writeSecretsLocked(name, rec); err != nil {
			return err
		}
		// The file never keeps a credential while a vault is active: it stores
		// the record with the secret fields zeroed.
		stored.ClientSecret = ""
		stored.Token = nil
	}

	servers := make(map[string]AuthRecord, len(s.servers)+1)
	for k, v := range s.servers {
		servers[k] = v
	}
	servers[name] = stored
	return s.writeLocked(servers)
}

// writeSecretsLocked pushes rec's credential half into the vault. Save is the
// only caller and holds s.mu, so the split happens inside the same locked region
// as the file write: a save is atomic from the caller's point of view and two
// concurrent saves cannot interleave their vault and file halves.
//
// An empty credential pair is a DELETE, not an empty store: "no secret" and "a
// secret that happens to be empty" must not be two different states in the
// vault, or a logout-then-login would leave a stale entry behind. DeleteSecret
// is idempotent by contract, so the delete of a never-written secret is a
// no-op rather than an error.
func (s *AuthStore) writeSecretsLocked(name string, rec AuthRecord) error {
	blob := authSecretBlob{ClientSecret: rec.ClientSecret, Token: authTokenCopy(rec.Token)}
	if blob.isEmpty() {
		if err := s.vault.DeleteSecret(secretNameFor(name)); err != nil {
			return fmt.Errorf("mcp: remove stored secret for %q: %w", name, err)
		}
		return nil
	}
	payload, err := encodeAuthSecretBlob(blob)
	if err != nil {
		return err
	}
	if err := s.vault.SetSecret(secretNameFor(name), payload); err != nil {
		return fmt.Errorf("mcp: store secret for %q: %w", name, err)
	}
	return nil
}

// Clear deletes name's record (token AND registration AND the auth_required
// flag). When the file has no records left it is removed. Disabled store: no-op.
//
// The deletion is by NAME only — no URL comparison — because this is the
// "forget everything about this server" primitive (the user ran `logout` or
// removed the entry) and a record left behind by an edited URL must go too.
// A missing record is a no-op, file untouched: nothing was stored, so the
// file is not ours to delete.
//
// With a vault the credential entry is deleted as well, and the deletion
// happens even when the FILE has no record for name: a half-finished migration
// or a Save whose file write failed can leave a blob with no file record, and
// "forget this server" has to mean the secret is gone. DeleteSecret is
// idempotent, so the common case (nothing stored) costs one no-op call.
//
// Order is vault first, then file, and a vault error is returned: fail closed.
// The record then stays, the secret stays, and the caller knows the logout did
// not happen — much better than a surface reporting success while the token is
// still live.
func (s *AuthStore) Clear(name string) error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()

	if s.vault != nil {
		if err := s.vault.DeleteSecret(secretNameFor(name)); err != nil {
			return fmt.Errorf("mcp: remove stored secret for %q: %w", name, err)
		}
	}

	if _, ok := s.servers[name]; !ok {
		return nil
	}
	servers := make(map[string]AuthRecord, len(s.servers))
	for k, v := range s.servers {
		if k == name {
			continue
		}
		servers[k] = v
	}
	if len(servers) == 0 {
		// Last record gone: the file goes with it. An empty envelope on disk
		// is a worse state than no file — NewAuthStore's reader treats a
		// missing file as "nothing stored", and leaving {"servers":{}} around
		// would keep a 0600 file in a directory the user believes is clean.
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("mcp: remove %s: %w", s.path, err)
		}
		s.invalidateLocked()
		return nil
	}
	return s.writeLocked(servers)
}

// TokenStore returns the transport.TokenStore bound to (name, url).
//
// The adapter is deliberately value-blind: mcp-go sees one server's token and
// nothing else, so a compromised or merely buggy transport cannot enumerate
// the user's other servers. Every call re-reads through the AuthStore, so a
// token saved by the OTHER process (or refreshed underneath us) is picked up
// on the next request without a restart — and with a vault active that read is
// live, so a token the other process refreshed is seen even when it never
// rewrote mcp-auth.json.
func (s *AuthStore) TokenStore(name, url string) transport.TokenStore {
	return &authTokenStore{store: s, name: name, url: url}
}

// authTokenStore is the per-server transport.TokenStore adapter: the (name,
// url) pair is captured once so mcp-go deals in tokens, not identities.
type authTokenStore struct {
	store *AuthStore
	name  string
	url   string
}

var _ transport.TokenStore = (*authTokenStore)(nil)

// GetToken returns the server's access token, or transport.ErrNoToken when the
// record is absent, bound to a different url, or holds no access token.
//
// The context is honoured before any I/O — the interface contract requires
// ctx.Err() rather than a token — and the result is a COPY: a pointer into the
// cache would let the transport mutate state that another goroutine (a
// refresh, a concurrent request, a Clear) is reading.
func (t *authTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, ok := t.store.recordFor(t.name, t.url)
	if !ok || rec.Token == nil || rec.Token.AccessToken == "" {
		// All three shapes mean the same thing to mcp-go: there is nothing to
		// present, so it should refresh or start the authorization flow.
		return nil, transport.ErrNoToken
	}
	tok := *rec.Token
	return &tok, nil
}

// SaveToken stores tok for this (name, url), preserving everything else the
// record already carries.
//
// mcp-go calls SaveToken after a token exchange, a refresh, or a dynamic
// client registration. The registration half (client_id/client_secret/
// resource_metadata_url/redirect_uri) is NOT in transport.Token, so writing
// tok alone would throw it away and force a fresh dynamic registration on
// every refresh — hence the read-modify-write. Only what transport.Token
// carries is taken from tok; URL is forced to the url this adapter is bound
// to, which is what makes a later URL edit invalidate the record.
//
// OWNERSHIP: what is persisted is a COPY of *tok, never the caller's pointer
// (finding F4: `rec.Token = tok` was the one aliasing write in this file, while
// Record and GetToken already hand out copies). mcp-go does not mutate a token
// after handing it over today, so the aliasing was latent rather than live —
// but a future mutation of the caller's object would rewrite what the store
// believes it persisted and race the record/revision logic, and the store has
// no way to see it coming. transport.Token is strings, one int64 and a
// time.Time, so the struct copy IS a deep copy. A nil tok still clears the
// stored token exactly as before, without dereferencing it.
//
// A vault-active store routes the final Save through the split, so this remains
// the single write path: the read-modify-write preserves the registration half
// in mcp-auth.json and the token (plus any client secret) lands in the vault.
func (t *authTokenStore) SaveToken(ctx context.Context, tok *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec, _ := t.store.Record(t.name) // raw accessor: the URL may be stale, it is overwritten below
	rec.URL = t.url
	rec.Token = authTokenCopy(tok)
	return t.store.Save(t.name, rec)
}

// authTokenCopy returns an independent copy of tok, or nil when tok is nil, so
// no caller-owned pointer is ever stored (see SaveToken's OWNERSHIP note).
func authTokenCopy(tok *transport.Token) *transport.Token {
	if tok == nil {
		return nil
	}
	cp := *tok
	return &cp
}

// recordFor returns name's record only when it is bound to url. Every
// url-aware read funnels through here so the binding rule lives in one place.
func (s *AuthStore) recordFor(name, url string) (AuthRecord, bool) {
	rec, ok := s.Record(name)
	if !ok || rec.URL != url {
		return AuthRecord{}, false
	}
	return rec, true
}

// migrateLegacySecrets is the one-time migration performed by NewAuthStore when
// a vault is active: every credential still sitting in mcp-auth.json is pushed
// into the vault and then stripped from the file, in ONE rewrite at the end.
//
// Design notes, because this is the only destructive step in the file:
//
//   - It is BEST EFFORT and cannot fail the constructor. A migration that could
//     not write a secret leaves that record exactly as it was (secrets and all)
//     and continues with the others. No credential is ever dropped: the delete
//     from the file only happens after the vault write succeeded.
//   - If a blob ALREADY exists in the vault and the file also carries secrets,
//     the file wins and overwrites the blob. The file is the state the running
//     binary has been reading until now, so it is the one that is current; the
//     stale blob is the residue of an interrupted migration.
//   - The single rewrite at the end is what keeps a crash window tiny: a crash
//     before it leaves the file untouched (secrets still readable, blob already
//     written — and the file wins on read), a crash after it leaves a clean
//     file whose secrets are all in the vault.
//   - A missing or malformed file is a no-op (readAuthFile reports empty).
//
// On a failed rewrite the cache is invalidated rather than left holding the
// stripped records: the file on disk still carries the secrets, and the
// file-wins rule in mergeSecrets must apply to them.
func (s *AuthStore) migrateLegacySecrets() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if len(s.servers) == 0 {
		return
	}

	migrated := 0
	for name, rec := range s.servers {
		if rec.ClientSecret == "" && rec.Token == nil {
			continue
		}
		blob := authSecretBlob{ClientSecret: rec.ClientSecret, Token: authTokenCopy(rec.Token)}
		payload, err := encodeAuthSecretBlob(blob)
		if err != nil {
			logWarnValueFree("legacy OAuth secret could not be encoded; leaving it in mcp-auth.json", name, err)
			continue
		}
		if err := s.vault.SetSecret(secretNameFor(name), payload); err != nil {
			logWarnValueFree("legacy OAuth secret could not be migrated to the vault; leaving it in mcp-auth.json", name, err)
			continue
		}
		rec.ClientSecret = ""
		rec.Token = nil
		s.servers[name] = rec
		migrated++
	}
	if migrated == 0 {
		return
	}
	if err := s.writeLocked(s.servers); err != nil {
		// The vault now holds copies and the file still holds the originals:
		// readable either way (the file wins), so this is a warning, not a
		// failure. Drop the cache so the next read sees the file as it is.
		logWarnValueFree("migrated OAuth secrets to the vault but could not rewrite mcp-auth.json; the file still holds copies", "", err)
		s.invalidateLocked()
	}
}

// refreshLocked reloads the cache when the file on disk is not the one we
// loaded. Callers must hold s.mu.
//
// The check is a stat, not a read: reads are the hot path (every transport
// request asks for a token) and this file is tiny but rarely changes. When
// the stamp matches, the cached map is returned untouched — that is also what
// makes Revision stable across repeated reads.
//
// Limitation, documented rather than papered over: mtime has filesystem
// resolution, so a writer that replaced a record with bytes of the SAME
// length within the same clock tick is invisible. SaveRawFile always renames a
// fresh temp file into place, which gives a new inode with a new mtime, and
// this store's own writes invalidate the cache outright, so the window is a
// theoretical one.
func (s *AuthStore) refreshLocked() {
	existed := false
	var modTime time.Time
	var size int64
	if fi, err := os.Stat(s.path); err == nil {
		existed, modTime, size = true, fi.ModTime(), fi.Size()
	}
	// An unreadable file (permission, I/O) is treated as absent: there is
	// nothing we can do with it, and pretending it holds records would be a
	// lie. The next write repairs the state.
	if s.loaded && s.existed == existed && s.size == size && s.modTime.Equal(modTime) {
		return
	}
	s.loaded, s.existed, s.modTime, s.size = true, existed, modTime, size
	s.servers = readAuthFile(s.path)
}

// invalidateLocked drops the cache so the next read re-stats and reloads.
// Callers must hold s.mu.
func (s *AuthStore) invalidateLocked() {
	s.loaded = false
	s.servers = nil
}

// writeLocked serialises servers and commits them via SaveRawFile (0600),
// then invalidates the cache: the file on disk is now the source of truth and
// the next read will stamp it. Callers must hold s.mu.
func (s *AuthStore) writeLocked(servers map[string]AuthRecord) error {
	content, err := json.Marshal(authEnvelope{Servers: servers})
	if err != nil {
		// Cannot carry a value: json.Marshal reports the Go type, and every
		// field of AuthRecord is a string/bool/*Token (all marshalable).
		return fmt.Errorf("mcp: encode %s: %w", s.path, err)
	}
	if err := SaveRawFile(s.path, content); err != nil {
		return err
	}
	s.invalidateLocked()
	return nil
}

// readAuthFile loads the record map, returning nil when nothing is readable.
//
// A missing file is the normal first-run state, not an error. A MALFORMED file
// is treated exactly the same — as empty — for one simple reason: this is a
// cache of secrets that can always be re-obtained by logging in again, and the
// alternative (surfacing a parse error through every read) would make a
// half-written or hand-edited file break the whole MCP surface including the
// servers that have nothing to do with auth. The next Save rewrites the file
// from the empty map and thereby repairs it; no partial parse is attempted
// because keeping bytes we do not understand would be worse than dropping
// them.
func readAuthFile(path string) map[string]AuthRecord {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // missing, unreadable, or a directory: nothing stored
	}
	var env authEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil // malformed: treated as empty, repaired by the next Save
	}
	return env.Servers
}

// copyRecord deep-copies the parts of a record a caller could mutate, i.e. the
// token (see authTokenCopy: the store's single token-copy rule).
func copyRecord(rec AuthRecord) AuthRecord {
	rec.Token = authTokenCopy(rec.Token)
	return rec
}

// usableToken mirrors mcp-go's OAuthHandler.getValidToken decision, with one
// guard that makes it consistent with our own GetToken: a token is usable when
// it carries an access token AND either is not expired or can be refreshed by
// its refresh token.
//
// The access-token guard is not decoration. mcp-go reaches its refresh branch
// only when GetToken returned a token (ErrNoToken is swallowed and turns into
// "start the authorization flow"), and GetToken reports ErrNoToken exactly
// when the access token is empty. So a refresh-token-only record is NOT
// usable, and claiming otherwise would make a surface say "authenticated"
// while the transport starts an interactive flow on the next request.
// A zero ExpiresAt means "no expiry known" (transport.Token.IsExpired), which
// mcp-go accepts, so it counts as non-expired.
func usableToken(tok *transport.Token) bool {
	if tok == nil || tok.AccessToken == "" {
		return false
	}
	return !tok.IsExpired() || tok.RefreshToken != ""
}

// authRevision is the FNV-1a hash of the record's JSON bytes. json.Marshal
// sorts map keys and strips the monotonic component of time.Time, so the same
// state always hashes the same — a caller may compare revisions across
// processes and across a restart.
func authRevision(rec AuthRecord) uint64 {
	data, err := json.Marshal(rec)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}
