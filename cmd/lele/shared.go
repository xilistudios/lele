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
	registerKeyringResolver(cfg)
	return config.LoadConfig(getConfigPath())
}

func defaultDBPath() string {
	return filepath.Join(config.GetLeleDir(), "lele.db")
}

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

const mcpSecretVaultSource = "mcp"

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

func sameLeleDir(requested, configured string) bool {
	req := normalizeLeleDir(requested)
	if req == "" {
		return false
	}
	return req == normalizeLeleDir(configured)
}

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

func newClientAuthManager(cfg *config.Config, leleDir string) (*channels.AuthManager, func(), error) {
	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		return nil, func() {}, fmt.Errorf("creating auth manager: %w", err)
	}

	dbPath := filepath.Join(leleDir, "lele.db")
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
		log.Printf("client: SQLite not available on this platform (%v); using JSON backends — PINs and clients will be stored in auth.json", dbErr)
		return authMgr, func() {}, nil
	} else {
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
