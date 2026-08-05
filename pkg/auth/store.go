package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

type AuthCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
	AuthMethod   string    `json:"auth_method"`
}

type AuthStore struct {
	Credentials map[string]*AuthCredential `json:"credentials"`
}

func (c *AuthCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

func (c *AuthCredential) NeedsRefresh() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

var (
	storeMu  sync.RWMutex
	authRepo *store.AuthRepo
)

// UseStore configures the auth package to persist credentials in the
// given SQLite repository. Until called (or if called with nil), the
// legacy auth.json file backend is used.
func UseStore(repo *store.AuthRepo) {
	storeMu.Lock()
	defer storeMu.Unlock()
	authRepo = repo
}

func currentRepo() *store.AuthRepo {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return authRepo
}

func authFilePath() string {
	return filepath.Join(config.GetLeleDir(), "auth.json")
}

// loadJSONStore loads the legacy auth.json file. It returns (nil, nil)
// when the file does not exist.
func loadJSONStore() (*AuthStore, error) {
	path := authFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("auth: read %s: %w", path, err)
	}
	var s AuthStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("auth: parse %s: %w", path, err)
	}
	if s.Credentials == nil {
		s.Credentials = make(map[string]*AuthCredential)
	}
	return &s, nil
}

// migrateFromJSONIfNeeded copies credentials from the legacy auth.json
// file into the SQLite repository the first time it is used. It is
// best-effort: failures are logged and swallowed so a migration problem
// never blocks authentication, except for ListCredentials errors which
// are propagated. The legacy file is never deleted or moved.
func migrateFromJSONIfNeeded(repo *store.AuthRepo) error {
	creds, err := repo.ListCredentials()
	if err != nil {
		return fmt.Errorf("auth: list credentials during migration check: %w", err)
	}
	if len(creds) > 0 {
		return nil // already migrated (or already has data)
	}

	legacy, err := loadJSONStore()
	if err != nil {
		logger.WarnCF("auth", "Failed to load legacy auth.json during migration", map[string]interface{}{"error": err.Error()})
		return nil //nolint:nilerr // best-effort migration: a broken legacy file must never block authentication
	}
	if legacy == nil {
		return nil // no legacy file, nothing to migrate
	}

	for key, cred := range legacy.Credentials {
		data, err := json.Marshal(cred)
		if err != nil {
			logger.WarnCF("auth", "Failed to marshal credential during migration", map[string]interface{}{"provider": key, "error": err.Error()})
			continue
		}
		if err := repo.SetCredential(key, string(data)); err != nil {
			logger.WarnCF("auth", "Failed to persist credential during migration", map[string]interface{}{"provider": key, "error": err.Error()})
			return nil //nolint:nilerr // best-effort migration: partial migration is acceptable, JSON fallback remains
		}
	}
	return nil
}

func LoadStore() (*AuthStore, error) {
	repo := currentRepo()
	if repo != nil {
		if err := migrateFromJSONIfNeeded(repo); err != nil {
			return nil, err
		}
		rows, err := repo.ListCredentials()
		if err != nil {
			return nil, fmt.Errorf("auth: list credentials: %w", err)
		}
		creds := make(map[string]*AuthCredential, len(rows))
		for key, raw := range rows {
			var cred AuthCredential
			if err := json.Unmarshal([]byte(raw), &cred); err != nil {
				return nil, fmt.Errorf("auth: unmarshal credential %q: %w", key, err)
			}
			creds[key] = &cred
		}
		return &AuthStore{Credentials: creds}, nil
	}

	// Legacy JSON backend
	s, err := loadJSONStore()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return &AuthStore{Credentials: make(map[string]*AuthCredential)}, nil
	}
	return s, nil
}

// SaveStore persists the given AuthStore. When the SQLite backend is
// active this behaves as an upsert: credentials present in store are
// written, but credentials absent from store are NOT removed from the
// repository.
func SaveStore(store *AuthStore) error {
	repo := currentRepo()
	if repo != nil {
		if store == nil {
			return nil
		}
		for key, cred := range store.Credentials {
			data, err := json.Marshal(cred)
			if err != nil {
				return fmt.Errorf("auth: marshal credential %q: %w", key, err)
			}
			if err := repo.SetCredential(key, string(data)); err != nil {
				return fmt.Errorf("auth: set credential %q: %w", key, err)
			}
		}
		return nil
	}

	// Legacy JSON backend
	path := authFilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func GetCredential(provider string) (*AuthCredential, error) {
	repo := currentRepo()
	if repo != nil {
		if err := migrateFromJSONIfNeeded(repo); err != nil {
			return nil, err
		}
		raw, found, err := repo.GetCredential(provider)
		if err != nil {
			return nil, fmt.Errorf("auth: get credential %q: %w", provider, err)
		}
		if !found {
			return nil, nil
		}
		var cred AuthCredential
		if err := json.Unmarshal([]byte(raw), &cred); err != nil {
			return nil, fmt.Errorf("auth: unmarshal credential %q: %w", provider, err)
		}
		return &cred, nil
	}

	// Legacy JSON backend
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	cred, ok := store.Credentials[provider]
	if !ok {
		return nil, nil
	}
	return cred, nil
}

func SetCredential(provider string, cred *AuthCredential) error {
	repo := currentRepo()
	if repo != nil {
		if err := migrateFromJSONIfNeeded(repo); err != nil {
			return err
		}
		data, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("auth: marshal credential %q: %w", provider, err)
		}
		if err := repo.SetCredential(provider, string(data)); err != nil {
			return fmt.Errorf("auth: set credential %q: %w", provider, err)
		}
		return nil
	}

	// Legacy JSON backend
	store, err := LoadStore()
	if err != nil {
		return err
	}
	store.Credentials[provider] = cred
	return SaveStore(store)
}

func DeleteCredential(provider string) error {
	repo := currentRepo()
	if repo != nil {
		if err := repo.DeleteCredential(provider); err != nil {
			return fmt.Errorf("auth: delete credential %q: %w", provider, err)
		}
		return nil
	}

	// Legacy JSON backend
	store, err := LoadStore()
	if err != nil {
		return err
	}
	delete(store.Credentials, provider)
	return SaveStore(store)
}

func DeleteAllCredentials() error {
	repo := currentRepo()
	if repo != nil {
		if err := repo.DeleteAllCredentials(); err != nil {
			return fmt.Errorf("auth: delete all credentials: %w", err)
		}
		return nil
	}

	// Legacy JSON backend
	path := authFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
