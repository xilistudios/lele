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

const vaultVersion = 1

var errVaultNotOpen = errors.New("keyring: vault is not open")

type vault struct {
	Version int       `json:"version"`
	Secrets []*Secret `json:"secrets"`
}

type vaultStamp struct {
	modTime time.Time
	size    int64
	exists  bool
}

func (s vaultStamp) matches(fi os.FileInfo) bool {
	return s.exists && fi.ModTime().Equal(s.modTime) && fi.Size() == s.size
}

type FileStore struct {
	path    string
	key     []byte
	secrets map[string]*Secret
	open    bool
	dirty   bool
	stamp   vaultStamp
	mu      sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{
		path:    path,
		secrets: make(map[string]*Secret),
	}
}

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

func (s *FileStore) IsOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

func (s *FileStore) loadLocked() (map[string]*Secret, vaultStamp, error) {
	secrets := make(map[string]*Secret)

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

func (s *FileStore) refreshLocked() error {
	if !s.open {
		return errVaultNotOpen
	}

	fi, err := os.Stat(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("keyring: stat vault: %w", err)
		}
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
		return err
	}
	s.secrets = secrets
	s.stamp = stamp
	s.dirty = false
	return nil
}

func (s *FileStore) restampLocked() {
	fi, err := os.Stat(s.path)
	if err != nil {
		s.stamp = vaultStamp{}
		return
	}
	s.stamp = vaultStamp{modTime: fi.ModTime(), size: fi.Size(), exists: true}
}

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

func (s *FileStore) Get(name string) (*Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(); err != nil {
		return nil, err
	}
	return s.secrets[name], nil
}

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

func (s *FileStore) Backend() string { return s.path }

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
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

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
	defer os.Remove(tmpPath)

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
