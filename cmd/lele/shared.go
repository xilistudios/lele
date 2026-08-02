package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
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
