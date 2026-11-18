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
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/mcp"
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
//
// The SAME Service also backs the MCP secret vault (registerMCPSecretVault), so
// one process never ends up with two keyring services over two different vault
// paths: the {{SECRET:}} resolver and the MCP OAuth store read and write one
// keyring.
func registerKeyringResolver(cfg *config.Config) {
	svc := newKeyringService(cfg)
	if svc == nil {
		config.RegisterKeyringResolver(nil)
	} else {
		config.RegisterKeyringResolver(func(name string) (string, error) {
			return svc.GetRaw(name)
		})
	}
	registerMCPSecretVault(svc)
}

// newKeyringService builds the process-wide keyring Service described by cfg,
// or returns nil when the keyring module is disabled (or there is no config).
//
// It is the single place that maps config to ServiceConfig: every caller that
// needs a Service (the resolver, the MCP vault) goes through here, so they
// cannot drift into resolving different vault files or different lele
// directories.
func newKeyringService(cfg *config.Config) *keyring.Service {
	if cfg == nil || !cfg.Keyring.Enabled {
		return nil
	}
	return keyring.NewService(keyring.ServiceConfig{
		Enabled:      cfg.Keyring.Enabled,
		VaultPath:    cfg.KeyringVaultPath(),
		Backend:      cfg.Keyring.Backend,
		AuditLogSize: cfg.Keyring.AuditLogSize,
		LeleDir:      config.GetLeleDir(),
	})
}

// mcpSecretVaultSource names the MCP subsystem in the secrets UIs: it is
// recorded as the CreatedBy of every secret the vault writes, so a user who
// opens the secrets page can tell an MCP OAuth token from their own keys.
const mcpSecretVaultSource = "mcp"

// registerMCPSecretVault installs the process-wide factory pkg/mcp consults
// when it opens an AuthStore (mcp.NewAuthStore). With one registered, the OAuth
// token and client secret of a remote MCP server live in the keyring —
// encrypted, agent-invisible (scope ["system"]) and shared across the lele
// processes of one installation — while mcp-auth.json keeps only the non-secret
// half of the record.
//
// svc is the Service built by newKeyringService. A nil (or disabled) svc
// registers a nil factory, which is pkg/mcp's documented "legacy plaintext
// mode": mcp-auth.json holds everything, exactly as before the vault existed.
// That degradation is deliberate — a machine with the keyring module switched
// off must keep working, not fail every login.
//
// The factory answers per DIRECTORY, because pkg/mcp only knows the lele
// directory it was handed. It returns nil when that directory is not the one
// svc was built for: a Store resolved for another installation (a test
// directory, a second -c dir) must never read or write THIS installation's
// keyring, so the mismatch degrades to plaintext for that store alone.
func registerMCPSecretVault(svc *keyring.Service) {
	if !svc.Enabled() {
		mcp.RegisterSecretVaultFactory(nil)
		return
	}
	mcp.RegisterSecretVaultFactory(func(leleDir string) mcp.SecretVault {
		if !sameLeleDir(leleDir, svc.LeleDir()) {
			return nil
		}
		return keyring.NewOpaqueVault(svc, mcpSecretVaultSource)
	})
}

// sameLeleDir reports whether two spellings name the same lele directory.
//
// pkg/mcp resolves its store from a directory STRING (mcp.Paths.LeleDir, which
// every surface derives from config.GetLeleDir) while the Service records the
// one it was built with, so the comparison must tolerate the difference between
// a relative and an absolute spelling of the same path. An empty request never
// matches: a store with no directory is disabled and gets no vault anyway.
func sameLeleDir(requested, configured string) bool {
	req := normalizeLeleDir(requested)
	if req == "" {
		return false
	}
	return req == normalizeLeleDir(configured)
}

// normalizeLeleDir canonicalises a lele directory for comparison: absolute,
// cleaned, "" when the input is empty. Symlinks are NOT resolved on purpose —
// that is I/O on a constructor path, and every caller of the comparison derives
// its path from config.GetLeleDir, which does not resolve them either.
func normalizeLeleDir(dir string) string {
	if dir = strings.TrimSpace(dir); dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return filepath.Clean(abs)
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
