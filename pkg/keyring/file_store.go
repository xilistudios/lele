package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// vaultVersion is the current on-disk vault schema version.
const vaultVersion = 1

// errVaultNotOpen is returned by every store operation once the vault has been
// closed (or before it was ever opened).
var errVaultNotOpen = errors.New("keyring: vault is not open")

// vault is the JSON structure serialized into the encrypted blob.
type vault struct {
	Version int       `json:"version"`
	Secrets []*Secret `json:"secrets"`
}

// vaultStamp identifies the on-disk version of the vault file a FileStore holds
// in memory. It follows the mtime+size stamp used by pkg/mcp's AuthStore: one
// stat is cheap enough to run on every operation and precise enough to notice
// any write that came from another process.
//
// Limitation, documented instead of papered over: mtime has filesystem
// resolution, so a writer that replaced the vault with a blob of the SAME size
// within the same clock tick is invisible. atomicWrite always renames a fresh
// temp file into place (new inode, new mtime) and our own writes re-stamp
// eagerly, so the window is theoretical.
type vaultStamp struct {
	modTime time.Time
	size    int64
	exists  bool
}

// matches reports whether fi describes the same file version as the stamp.
func (s vaultStamp) matches(fi os.FileInfo) bool {
	return s.exists && fi.ModTime().Equal(s.modTime) && fi.Size() == s.size
}

// FileStore is an AES-256-GCM encrypted vault persisted to a single file.
//
// On-disk format:
//
//	[12-byte nonce][ciphertext (JSON vault)][16-byte GCM tag]
//
// No salt is required because the master key comes from the OS keychain or a
// protected key file rather than being derived from a passphrase.
//
// # Cross-process freshness
//
// More than one lele process can hold the vault open at a time (the TUI and the
// gateway are the common pair). Every operation therefore starts by re-stating
// the vault file and, when it changed, replaces the in-memory secrets with the
// on-disk state before doing anything else. That is why all operations take the
// store's write lock: a refresh mutates state, so read-only methods cannot run
// under a shared lock without a second lock upgrade.
//
// The semantics stay last-writer-wins across processes, exactly like
// pkg/mcp/auth_store.go: refreshing before a mutation shrinks the race window to
// the few microseconds between the stat and the rename, but it does not remove
// it. Two processes writing the same secret at the same instant still end with
// one of the two values, not a merge.
type FileStore struct {
	path    string
	key     []byte
	secrets map[string]*Secret
	open    bool
	// dirty records that the in-memory vault holds a change that has not been
	// flushed yet, i.e. that a refresh would discard local work.
	dirty bool
	stamp vaultStamp
	mu    sync.Mutex
}

// NewFileStore creates a FileStore backed by the given file path.
func NewFileStore(path string) *FileStore {
	return &FileStore{
		path:    path,
		secrets: make(map[string]*Secret),
	}
}

// Open decrypts the vault file using the provided 32-byte key. A missing file
// is treated as an empty vault. A file that cannot be decrypted (e.g. the
// master key was lost) returns an error so the caller can decide to
// reinitialize.
func (s *FileStore) Open(key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(key) != masterKeySize {
		return fmt.Errorf("keyring: master key must be %d bytes, got %d", masterKeySize, len(key))
	}

	s.key = make([]byte, masterKeySize)
	copy(s.key, key)
	s.secrets = make(map[string]*Secret)
	s.dirty = false
	s.stamp = vaultStamp{}

	secrets, stamp, err := s.loadLocked()
	if err != nil {
		s.zeroKeyLocked()
		return err
	}
	s.secrets = secrets
	s.stamp = stamp
	s.open = true
	return nil
}

// Close zeroes the in-memory key and clears the decrypted secrets.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zeroKeyLocked()
	s.secrets = make(map[string]*Secret)
	s.stamp = vaultStamp{}
	s.dirty = false
	s.open = false
	return nil
}

func (s *FileStore) zeroKeyLocked() {
	for i := range s.key {
		s.key[i] = 0
	}
	s.key = nil
}

// IsOpen returns true if the vault is decrypted and ready.
func (s *FileStore) IsOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// loadLocked reads, decrypts and decodes the vault file. The returned stamp
// describes the exact bytes that were loaded, so the caller can cache them and
// detect the next external write with a single stat.
//
// A missing (or empty) file yields an empty map and a zero stamp. Callers must
// hold s.mu and must have a master key loaded.
func (s *FileStore) loadLocked() (map[string]*Secret, vaultStamp, error) {
	secrets := make(map[string]*Secret)

	// Stat before reading so the stamp is never NEWER than the bytes we decode:
	// if a writer lands between the two calls the worst case is one redundant
	// reload, never a stale vault that looks fresh.
	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return secrets, vaultStamp{}, nil
		}
		return nil, vaultStamp{}, fmt.Errorf("keyring: read vault: %w", err)
	}
	stamp := vaultStamp{modTime: fi.ModTime(), size: fi.Size(), exists: true}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Deleted between the stat and the read.
			return secrets, vaultStamp{}, nil
		}
		return nil, stamp, fmt.Errorf("keyring: read vault: %w", err)
	}
	if len(data) == 0 {
		return secrets, stamp, nil
	}

	plaintext, err := decrypt(s.key, data)
	if err != nil {
		return nil, stamp, fmt.Errorf("keyring: decrypt vault (master key may have changed): %w", err)
	}
	var v vault
	if err := json.Unmarshal(plaintext, &v); err != nil {
		return nil, stamp, fmt.Errorf("keyring: corrupt vault: %w", err)
	}
	for _, sec := range v.Secrets {
		if sec != nil && sec.Name != "" {
			secrets[sec.Name] = sec
		}
	}
	return secrets, stamp, nil
}

// refreshLocked makes the in-memory vault agree with the file on disk when
// another process wrote to it. Callers must hold s.mu.
//
// The fast path is a single stat: when the stamp matches, the cached map is
// returned untouched. That is the normal case, including for a process that is
// the only writer.
func (s *FileStore) refreshLocked() error {
	if !s.open {
		return errVaultNotOpen
	}

	fi, err := os.Stat(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("keyring: stat vault: %w", err)
		}
		// The vault we cached was deleted from outside. Treat that as an empty
		// vault (the same self-healing pkg/mcp applies to mcp-auth.json) so the
		// next flush recreates the file from the current state instead of
		// resurrecting secrets somebody deliberately removed.
		if s.stamp.exists {
			s.secrets = make(map[string]*Secret)
			s.stamp = vaultStamp{}
			s.dirty = false
		}
		return nil
	}
	if s.stamp.matches(fi) {
		return nil
	}

	secrets, stamp, err := s.loadLocked()
	if err != nil {
		// Deliberately NOT falling back to an empty vault: atomicWrite renames a
		// complete temp file into place, so a failed decrypt or decode after an
		// external change means real corruption or a master key mismatch. The
		// stamp stays at its old value so every following operation reports the
		// same error rather than silently serving — or overwriting — data we
		// cannot read.
		return err
	}
	s.secrets = secrets
	s.stamp = stamp
	s.dirty = false
	return nil
}

// restampLocked records the on-disk version of the vault we just wrote so the
// next operation takes the fast path. Callers must hold s.mu.
//
// A stat failure here is not fatal: the write itself already succeeded, and
// clearing the stamp only means the next operation re-reads the file.
func (s *FileStore) restampLocked() {
	fi, err := os.Stat(s.path)
	if err != nil {
		s.stamp = vaultStamp{}
		return
	}
	s.stamp = vaultStamp{modTime: fi.ModTime(), size: fi.Size(), exists: true}
}

// Set stores or updates a secret in memory. Callers persist the change with
// Flush.
func (s *FileStore) Set(secret *Secret) error {
	if secret == nil || secret.Name == "" {
		return errors.New("keyring: secret name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return err
	}
	s.secrets[secret.Name] = secret
	s.dirty = true
	return nil
}

// Get retrieves a secret by name. A name that is not stored returns (nil, nil);
// an error means the vault could not be refreshed (not open, unreadable, or
// written by a process using a different master key).
func (s *FileStore) Get(name string) (*Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	return s.secrets[name], nil
}

// Delete removes a secret by name. It reports false when the name is not
// stored. The change is persisted by Flush.
func (s *FileStore) Delete(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return false, err
	}
	if _, ok := s.secrets[name]; !ok {
		return false, nil
	}
	delete(s.secrets, name)
	s.dirty = true
	return true, nil
}

// List returns metadata for all secrets, sorted by name.
func (s *FileStore) List() ([]SecretMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	out := make([]SecretMeta, 0, len(s.secrets))
	for _, sec := range s.secrets {
		out = append(out, sec.Meta())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Search finds secrets matching a query across name, tags, and description.
// An empty query returns all secrets.
func (s *FileStore) Search(query string) ([]SecretMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]SecretMeta, 0, len(s.secrets))
	for _, sec := range s.secrets {
		if q == "" || matchesQuery(sec, q) {
			out = append(out, sec.Meta())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func matchesQuery(sec *Secret, q string) bool {
	if strings.Contains(strings.ToLower(sec.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(sec.Description), q) {
		return true
	}
	for _, tag := range sec.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// Flush re-encrypts the vault and atomically writes it to disk.
//
// A pending in-memory mutation (dirty) is written as-is: refreshing here would
// clobber exactly the change the caller is trying to persist. Without one,
// external changes are absorbed first so a bare Flush can never roll another
// process back to the state we happened to have cached.
func (s *FileStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return errVaultNotOpen
	}
	if !s.dirty {
		if err := s.refreshLocked(); err != nil {
			return err
		}
	}

	v := vault{Version: vaultVersion, Secrets: make([]*Secret, 0, len(s.secrets))}
	for _, sec := range s.secrets {
		v.Secrets = append(v.Secrets, sec)
	}
	sort.Slice(v.Secrets, func(i, j int) bool { return v.Secrets[i].Name < v.Secrets[j].Name })

	plaintext, err := json.Marshal(&v)
	if err != nil {
		return fmt.Errorf("keyring: marshal vault: %w", err)
	}
	ciphertext, err := encrypt(s.key, plaintext)
	if err != nil {
		return fmt.Errorf("keyring: encrypt vault: %w", err)
	}
	if err := atomicWrite(s.path, ciphertext); err != nil {
		return err
	}
	s.dirty = false
	s.restampLocked()
	return nil
}

// Backend returns the vault file path.
func (s *FileStore) Backend() string { return s.path }

// encrypt seals plaintext with AES-256-GCM, prepending the random nonce.
func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// Seal appends the ciphertext+tag to the nonce so the output is
	// [nonce][ciphertext][tag].
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt opens an AES-256-GCM blob produced by encrypt.
func decrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// atomicWrite writes data to path via a temp file + rename so a crash mid-write
// never leaves a truncated vault.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".keyring-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
