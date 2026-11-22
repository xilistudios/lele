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

const masterKeySize = 32

type KeyProvider interface {
	GetKey() ([]byte, error)
	Backend() string
	Available() bool
}

const (
	BackendKeychain = "keychain"
	BackendFile     = "file"
	BackendAuto     = "auto"
)

type OSKeychainProvider struct {
	service string
	account string
}

func NewOSKeychainProvider() *OSKeychainProvider {
	return &OSKeychainProvider{
		service: "lele",
		account: "keyring-master-key",
	}
}

func (p *OSKeychainProvider) GetKey() ([]byte, error) {
	encoded, err := keyring.Get(p.service, p.account)
	if err == nil {
		if key, decErr := base64.StdEncoding.DecodeString(encoded); decErr == nil && len(key) == masterKeySize {
			return key, nil
		}
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

func (p *OSKeychainProvider) Backend() string { return BackendKeychain }

func (p *OSKeychainProvider) Available() bool {
	_, err := keyring.Get(p.service, p.account)
	if err == nil {
		return true
	}
	return errors.Is(err, keyring.ErrNotFound)
}

type FileKeyProvider struct {
	path string
}

func NewFileKeyProvider(path string) *FileKeyProvider {
	return &FileKeyProvider{path: path}
}

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

func (p *FileKeyProvider) Backend() string { return BackendFile }

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

func NewKeyProvider(leleDir, backend string) KeyProvider {
	keyFilePath := filepath.Join(leleDir, "keyring.key")

	switch strings.ToLower(strings.TrimSpace(backend)) {
	case BackendFile:
		return NewFileKeyProvider(keyFilePath)
	case BackendKeychain:
		return NewOSKeychainProvider()
	default:
		osProvider := NewOSKeychainProvider()
		if osProvider.Available() {
			return osProvider
		}
		return NewFileKeyProvider(keyFilePath)
	}
}

func generateMasterKey() ([]byte, error) {
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("keyring: generate master key: %w", err)
	}
	return key, nil
}
