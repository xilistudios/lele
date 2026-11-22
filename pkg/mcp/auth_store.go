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

const authFileName = "mcp-auth.json"

type AuthRecord struct {
	URL                 string           `json:"url"`
	ClientID            string           `json:"client_id,omitempty"`
	ClientSecret        string           `json:"client_secret,omitempty"`
	ResourceMetadataURL string           `json:"resource_metadata_url,omitempty"`
	RedirectURI         string           `json:"redirect_uri,omitempty"`
	AuthRequired        bool             `json:"auth_required,omitempty"`
	Token               *transport.Token `json:"token,omitempty"`
}

type authEnvelope struct {
	Servers map[string]AuthRecord `json:"servers"`
}

type AuthStore struct {
	mu   sync.Mutex
	path string

	vault SecretVault

	loaded  bool
	existed bool
	modTime time.Time
	size    int64

	servers map[string]AuthRecord
}

type AuthStoreOption func(*AuthStore)

func WithSecretVault(v SecretVault) AuthStoreOption {
	return func(s *AuthStore) { s.vault = v }
}

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

func (s *AuthStore) Enabled() bool {
	return s != nil && s.path != ""
}

func (s *AuthStore) Record(name string) (AuthRecord, bool) {
	return s.resolveRecord(name)
}

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

func (s *AuthStore) mergeSecrets(name string, rec AuthRecord) AuthRecord {
	rec = copyRecord(rec)
	if s.vault == nil {
		return rec
	}
	if rec.ClientSecret != "" || rec.Token != nil {
		return rec
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

func (s *AuthStore) Authenticated(name, url string) bool {
	rec, ok := s.recordFor(name, url)
	return ok && usableToken(rec.Token)
}

func (s *AuthStore) AuthRequired(name, url string) bool {
	rec, ok := s.recordFor(name, url)
	return ok && rec.AuthRequired
}

func (s *AuthStore) Revision(name string) uint64 {
	rec, ok := s.resolveRecord(name)
	if !ok {
		return 0
	}
	return authRevision(rec)
}

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
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("mcp: remove %s: %w", s.path, err)
		}
		s.invalidateLocked()
		return nil
	}
	return s.writeLocked(servers)
}

func (s *AuthStore) TokenStore(name, url string) transport.TokenStore {
	return &authTokenStore{store: s, name: name, url: url}
}

type authTokenStore struct {
	store *AuthStore
	name  string
	url   string
}

var _ transport.TokenStore = (*authTokenStore)(nil)

func (t *authTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, ok := t.store.recordFor(t.name, t.url)
	if !ok || rec.Token == nil || rec.Token.AccessToken == "" {
		return nil, transport.ErrNoToken
	}
	tok := *rec.Token
	return &tok, nil
}

func (t *authTokenStore) SaveToken(ctx context.Context, tok *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec, _ := t.store.Record(t.name)
	rec.URL = t.url
	rec.Token = authTokenCopy(tok)
	return t.store.Save(t.name, rec)
}

func authTokenCopy(tok *transport.Token) *transport.Token {
	if tok == nil {
		return nil
	}
	cp := *tok
	return &cp
}

func (s *AuthStore) recordFor(name, url string) (AuthRecord, bool) {
	rec, ok := s.Record(name)
	if !ok || rec.URL != url {
		return AuthRecord{}, false
	}
	return rec, true
}

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
		logWarnValueFree("migrated OAuth secrets to the vault but could not rewrite mcp-auth.json; the file still holds copies", "", err)
		s.invalidateLocked()
	}
}

func (s *AuthStore) refreshLocked() {
	existed := false
	var modTime time.Time
	var size int64
	if fi, err := os.Stat(s.path); err == nil {
		existed, modTime, size = true, fi.ModTime(), fi.Size()
	}
	if s.loaded && s.existed == existed && s.size == size && s.modTime.Equal(modTime) {
		return
	}
	s.loaded, s.existed, s.modTime, s.size = true, existed, modTime, size
	s.servers = readAuthFile(s.path)
}

func (s *AuthStore) invalidateLocked() {
	s.loaded = false
	s.servers = nil
}

func (s *AuthStore) writeLocked(servers map[string]AuthRecord) error {
	content, err := json.Marshal(authEnvelope{Servers: servers})
	if err != nil {
		return fmt.Errorf("mcp: encode %s: %w", s.path, err)
	}
	if err := SaveRawFile(s.path, content); err != nil {
		return err
	}
	s.invalidateLocked()
	return nil
}

func readAuthFile(path string) map[string]AuthRecord {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var env authEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil
	}
	return env.Servers
}

func copyRecord(rec AuthRecord) AuthRecord {
	rec.Token = authTokenCopy(rec.Token)
	return rec
}

func usableToken(tok *transport.Token) bool {
	if tok == nil || tok.AccessToken == "" {
		return false
	}
	return !tok.IsExpired() || tok.RefreshToken != ""
}

func authRevision(rec AuthRecord) uint64 {
	data, err := json.Marshal(rec)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}
