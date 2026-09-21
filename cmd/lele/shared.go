package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/store"
)

//go:generate cp -r ../../workspace .
//go:embed workspace web/dist
var embeddedFiles embed.FS

const logo = "🦞"

func getConfigPath() string {
	return config.DefaultConfigPath()
}

func getLeleDir() string {
	return config.GetLeleDir()
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(getConfigPath())
	if err != nil {
		return nil, err
	}
	// Register the keyring resolver so {{SECRET:name}} placeholders can be
	// resolved, then reload so secret-backed config values are populated.
	registerKeyringResolver(cfg)
	return config.LoadConfig(getConfigPath())
}

// defaultDBPath returns the default path for the SQLite store database.
func defaultDBPath() string {
	return filepath.Join(config.GetLeleDir(), "lele.db")
}

// openSharedStore opens the shared SQLite store at the given path.
// On success it returns a ready-to-use store and an idempotent cleanup
// function that closes it. On failure the store is nil and cleanup is
// a no-op.
//
// Production callers must use this helper so every DB open goes through
// a single code path; the returned store is then injected into
// agent.NewAgentLoopWithStore, which takes ownership and closes it on
// shutdown. Callers must NOT close the store themselves.
func openSharedStore(dbPath string, component string) (s *store.Store, cleanup func(), err error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, func() {}, err
	}
	var once sync.Once
	return st, func() {
		once.Do(func() { st.Close() })
	}, nil
}

// registerKeyringResolver installs a config-level resolver that reads secret
// values from the keyring. The service is created lazily and performs no I/O
// until a {{SECRET:}} placeholder is actually resolved.
func registerKeyringResolver(cfg *config.Config) {
	if cfg == nil || !cfg.Keyring.Enabled {
		config.RegisterKeyringResolver(nil)
		return
	}
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      cfg.Keyring.Enabled,
		VaultPath:    cfg.KeyringVaultPath(),
		Backend:      cfg.Keyring.Backend,
		AuditLogSize: cfg.Keyring.AuditLogSize,
		LeleDir:      config.GetLeleDir(),
	})
	config.RegisterKeyringResolver(func(name string) (string, error) {
		return svc.GetRaw(name)
	})
}

// newClientAuthManager creates an AuthManager wired to the shared SQLite store
// (via SetStore) when the database can be opened. When the DB is unavailable
// (e.g. mips64 without cgo, corrupted path), it falls back to the JSON-backed
// AuthManager with a warning, exactly preserving the pre-refactor behaviour of
// client.go.
//
// The returned cleanup function must be called (deferred) before the caller
// returns. It is always non-nil and safe to call even when the store was not
// opened.
func newClientAuthManager(cfg *config.Config, leleDir string) (*channels.AuthManager, func(), error) {
	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		return nil, func() {}, fmt.Errorf("creating auth manager: %w", err)
	}

	dbPath := filepath.Join(leleDir, "lele.db")
	// A missing lele directory is not a corrupted-database condition:
	// onboarding reaches the PIN step before the config save creates the
	// directory, so create it here (same 0755 as config.SaveEditableDocument)
	// rather than refusing to mint a PIN on a fresh install. Anything the
	// OS still refuses to create is a real error and is reported as one.
	if leleDir != "" {
		if mkErr := os.MkdirAll(leleDir, 0755); mkErr != nil {
			return nil, func() {}, fmt.Errorf("creating lele dir %s: %w", leleDir, mkErr)
		}
	}
	s, cleanup, dbErr := openSharedStore(dbPath, "client-auth")
	if dbErr == nil {
		authMgr.SetStore(s.NativeClients())
		return authMgr, cleanup, nil
	} else if errors.Is(dbErr, store.ErrUnsupportedPlatform) {
		// Platform lacks SQLite (e.g. linux/mips64). JSON is the real
		// backend here, so PINs minted via the JSON path ARE redeemable.
		log.Printf("client: SQLite not available on this platform (%v); using JSON backends — PINs and clients will be stored in auth.json", dbErr)
		return authMgr, func() {}, nil
	} else {
		// SQLite is supported but the database could not be opened
		// (corrupted file, permissions, disk full, …). Minting a PIN
		// here would produce a dead PIN: the gateway uses SQLite and
		// will never find it in native_clients.json. Fail explicitly.
		return nil, func() {}, fmt.Errorf("opening %s: %w — refusing to mint a PIN that the gateway cannot redeem; fix the database or run on a no-SQLite build", dbPath, dbErr)
	}
}

func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

func copyEmbeddedToTarget(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("Failed to create target directory: %w", err)
	}

	err := fs.WalkDir(embeddedFiles, "workspace", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		data, err := embeddedFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Failed to read embedded file %s: %w", path, err)
		}

		new_path, err := filepath.Rel("workspace", path)
		if err != nil {
			return fmt.Errorf("Failed to get relative path for %s: %v\n", path, err)
		}

		targetPath := filepath.Join(targetDir, new_path)

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("Failed to create directory %s: %w", filepath.Dir(targetPath), err)
		}

		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return fmt.Errorf("Failed to write file %s: %w", targetPath, err)
		}

		return nil
	})

	return err
}

func createWorkspaceTemplates(workspace string) {
	err := copyEmbeddedToTarget(workspace)
	if err != nil {
		fmt.Printf("Error copying workspace templates: %v\n", err)
	}
}
