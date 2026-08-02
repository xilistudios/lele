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
)

// vaultVersion is the current on-disk vault schema version.
const vaultVersion = 1

// vault is the JSON structure serialized into the encrypted blob.
type vault struct {
	Version int       `json:"version"`
	Secrets []*Secret `json:"secrets"`
}

// FileStore is an AES-256-GCM encrypted vault persisted to a single file.
//
// On-disk format:
//
//	[12-byte nonce][ciphertext (JSON vault)][16-byte GCM tag]
//
// No salt is required because the master key comes from the OS keychain or a
// protected key file rather than being derived from a passphrase.
type FileStore struct {
	path    string
	key     []byte
	secrets map[string]*Secret
	open    bool
	mu      sync.RWMutex
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

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.open = true
			return nil
		}
		s.zeroKeyLocked()
		return fmt.Errorf("keyring: read vault: %w", err)
	}

	if len(data) > 0 {
		plaintext, err := decrypt(s.key, data)
		if err != nil {
			s.zeroKeyLocked()
			return fmt.Errorf("keyring: decrypt vault (master key may have changed): %w", err)
		}
		var v vault
		if err := json.Unmarshal(plaintext, &v); err != nil {
			s.zeroKeyLocked()
			return fmt.Errorf("keyring: corrupt vault: %w", err)
		}
		for _, sec := range v.Secrets {
			if sec != nil && sec.Name != "" {
				s.secrets[sec.Name] = sec
			}
		}
	}

	s.open = true
	return nil
}

// Close zeroes the in-memory key and clears the decrypted secrets.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zeroKeyLocked()
	s.secrets = make(map[string]*Secret)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.open
}

// Set stores or updates a secret.
func (s *FileStore) Set(secret *Secret) error {
	if secret == nil || secret.Name == "" {
		return errors.New("keyring: secret name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return errors.New("keyring: vault is not open")
	}
	s.secrets[secret.Name] = secret
	return nil
}

// Get retrieves a secret by name.
func (s *FileStore) Get(name string) (*Secret, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.open {
		return nil, false
	}
	sec, ok := s.secrets[name]
	return sec, ok
}

// Delete removes a secret by name.
func (s *FileStore) Delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false
	}
	if _, ok := s.secrets[name]; !ok {
		return false
	}
	delete(s.secrets, name)
	return true
}

// List returns metadata for all secrets, sorted by name.
func (s *FileStore) List() []SecretMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SecretMeta, 0, len(s.secrets))
	for _, sec := range s.secrets {
		out = append(out, sec.Meta())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Search finds secrets matching a query across name, tags, and description.
// An empty query returns all secrets.
func (s *FileStore) Search(query string) []SecretMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]SecretMeta, 0, len(s.secrets))
	for _, sec := range s.secrets {
		if q == "" || matchesQuery(sec, q) {
			out = append(out, sec.Meta())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
func (s *FileStore) Flush() error {
	s.mu.RLock()
	if !s.open {
		s.mu.RUnlock()
		return errors.New("keyring: vault is not open")
	}
	v := vault{Version: vaultVersion, Secrets: make([]*Secret, 0, len(s.secrets))}
	for _, sec := range s.secrets {
		v.Secrets = append(v.Secrets, sec)
	}
	sort.Slice(v.Secrets, func(i, j int) bool { return v.Secrets[i].Name < v.Secrets[j].Name })
	key := s.key
	s.mu.RUnlock()

	plaintext, err := json.Marshal(&v)
	if err != nil {
		return fmt.Errorf("keyring: marshal vault: %w", err)
	}
	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		return fmt.Errorf("keyring: encrypt vault: %w", err)
	}
	return atomicWrite(s.path, ciphertext)
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
