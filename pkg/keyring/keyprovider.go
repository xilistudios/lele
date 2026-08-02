package keyring

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// masterKeySize is the size of the AES-256 master key in bytes.
const masterKeySize = 32

// KeyProvider retrieves or creates the master encryption key used to protect
// the secret vault. Implementations must be safe for the key to be generated
// lazily on first use.
type KeyProvider interface {
	// GetKey returns the 32-byte master key, creating and persisting it if
	// necessary.
	GetKey() ([]byte, error)
	// Backend returns the active backend name ("keychain" or "file").
	Backend() string
	// Available reports whether this provider can function in the current
	// environment.
	Available() bool
}

// Backend name constants.
const (
	BackendKeychain = "keychain"
	BackendFile     = "file"
	BackendAuto     = "auto"
)

// OSKeychainProvider stores the master key in the operating system's native
// secret store (macOS Keychain, GNOME Keyring / KWallet via the freedesktop
// Secret Service, or Windows Credential Manager).
type OSKeychainProvider struct {
	service string // keyring service name, e.g. "lele"
	account string // keyring account name, e.g. "keyring-master-key"
}

// NewOSKeychainProvider creates an OSKeychainProvider with the default
// service/account identifiers.
func NewOSKeychainProvider() *OSKeychainProvider {
	return &OSKeychainProvider{
		service: "lele",
		account: "keyring-master-key",
	}
}

// GetKey returns the master key from the OS keychain, generating and storing a
// new one if it does not yet exist.
func (p *OSKeychainProvider) GetKey() ([]byte, error) {
	encoded, err := keyring.Get(p.service, p.account)
	if err == nil {
		if key, decErr := base64.StdEncoding.DecodeString(encoded); decErr == nil && len(key) == masterKeySize {
			return key, nil
		}
		// Stored entry is corrupt or the wrong size; fall through and regenerate.
	} else if !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("keyring: keychain read failed: %w", err)
	}

	key, err := generateMasterKey()
	if err != nil {
		return nil, err
	}
	if err := keyring.Set(p.service, p.account, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, fmt.Errorf("keyring: store master key in keychain: %w", err)
	}
	return key, nil
}

// Backend returns "keychain".
func (p *OSKeychainProvider) Backend() string { return BackendKeychain }

// Available probes the OS keychain. It returns true when the keychain is
// reachable (even if no entry exists yet) and false when the underlying
// secret service is unavailable (e.g. no D-Bus session on a headless host).
func (p *OSKeychainProvider) Available() bool {
	_, err := keyring.Get(p.service, p.account)
	if err == nil {
		return true
	}
	// ErrNotFound means the keychain responded but has no entry yet.
	return errors.Is(err, keyring.ErrNotFound)
}

// FileKeyProvider stores the master key in a local file. It is the fallback
// for systems without a usable OS keychain (containers, headless servers).
type FileKeyProvider struct {
	path string // e.g. ~/.lele/keyring.key
}

// NewFileKeyProvider creates a FileKeyProvider that stores the master key at
// the given path.
func NewFileKeyProvider(path string) *FileKeyProvider {
	return &FileKeyProvider{path: path}
}

// GetKey returns the master key from the key file, generating and writing a
// new one (with 0600 permissions) if it does not yet exist.
func (p *FileKeyProvider) GetKey() ([]byte, error) {
	data, err := os.ReadFile(p.path)
	if err == nil {
		if len(data) == masterKeySize {
			return data, nil
		}
		if decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); decErr == nil && len(decoded) == masterKeySize {
			return decoded, nil
		}
		return nil, fmt.Errorf("keyring: key file %s has invalid contents", p.path)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("keyring: read key file: %w", err)
	}

	key, err := generateMasterKey()
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(p.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("keyring: create key dir: %w", err)
		}
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(p.path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("keyring: write key file: %w", err)
	}
	return key, nil
}

// Backend returns "file".
func (p *FileKeyProvider) Backend() string { return BackendFile }

// Available reports whether the key file's parent directory can be created.
func (p *FileKeyProvider) Available() bool {
	dir := filepath.Dir(p.path)
	if dir == "" || dir == "." {
		return true
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	return true
}

// NewKeyProvider returns the best available key provider for the given lele
// directory and configured backend ("auto", "keychain", or "file").
//
// In "auto" mode the OS keychain is preferred and the file backend is used
// only when no keychain is detected. An explicit "keychain" or "file" backend
// is honored directly.
func NewKeyProvider(leleDir, backend string) KeyProvider {
	keyFilePath := filepath.Join(leleDir, "keyring.key")

	switch strings.ToLower(strings.TrimSpace(backend)) {
	case BackendFile:
		return NewFileKeyProvider(keyFilePath)
	case BackendKeychain:
		return NewOSKeychainProvider()
	default: // "auto" or empty
		osProvider := NewOSKeychainProvider()
		if osProvider.Available() {
			return osProvider
		}
		return NewFileKeyProvider(keyFilePath)
	}
}

// generateMasterKey returns a cryptographically random 32-byte key.
func generateMasterKey() ([]byte, error) {
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("keyring: generate master key: %w", err)
	}
	return key, nil
}
