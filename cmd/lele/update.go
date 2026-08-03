package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/update"
)

// updateCmd implements `lele update` for self-updating the binary/service.
func updateCmd() {
	args := os.Args[2:]

	var (
		checkOnly bool
		assumeYes bool
		noRestart bool
		force     bool
		rollback  bool
		pinVer    string
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "-c":
			checkOnly = true
		case "--yes", "-y":
			assumeYes = true
		case "--no-restart":
			noRestart = true
		case "--force", "-f":
			force = true
		case "--rollback":
			rollback = true
		case "--version", "-v":
			if i+1 < len(args) {
				pinVer = args[i+1]
				i++
			} else {
				fmt.Println("Error: --version requires a value (e.g. --version v0.9.0)")
				os.Exit(1)
			}
		case "--help", "-h":
			printUpdateHelp()
			return
		default:
			fmt.Printf("Unknown option: %s\n", args[i])
			printUpdateHelp()
			os.Exit(1)
		}
	}

	updater, cfg, err := buildUpdater()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if cfg != nil && !cfg.Updates.Enabled {
		fmt.Println("Self-update is disabled in config (updates.enabled = false).")
		os.Exit(1)
	}

	if err := update.CheckEnvironment(); err != nil {
		fmt.Printf("Cannot self-update: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if rollback {
		runRollback(ctx, updater)
		return
	}

	if checkOnly {
		runCheck(ctx, updater)
		return
	}

	runApply(ctx, updater, pinVer, assumeYes, noRestart, force)
}

func buildUpdater() (*update.Updater, *config.Config, error) {
	// Config is optional; fall back to defaults if it can't be loaded.
	cfg, _ := loadConfig()

	repo := ""
	if cfg != nil {
		repo = cfg.Updates.Repo
	}

	backupDir := filepath.Join(getLeleDir(), "backups")
	updater := update.NewUpdater(repo, backupDir, version)
	return updater, cfg, nil
}

func runCheck(ctx context.Context, updater *update.Updater) {
	fmt.Printf("Current: %s\n", formatVersion())
	fmt.Println("Checking for updates...")

	info, err := updater.Check(ctx)
	if err != nil {
		fmt.Printf("Error checking for updates: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Latest:  %s", info.Latest)
	if !info.PublishedAt.IsZero() {
		fmt.Printf(" (published %s)", info.PublishedAt.Format("2006-01-02"))
	}
	fmt.Println()

	if info.UpdateAvailable {
		fmt.Println("Update available. Run 'lele update' to install.")
	} else {
		fmt.Println("✓ Already up to date.")
	}
}

func runApply(ctx context.Context, updater *update.Updater, pinVer string, assumeYes, noRestart, force bool) {
	if update.IsDevBuild(version) && !force {
		fmt.Println("This is a local/dev build (version \"dev\").")
		fmt.Println("Self-update would replace it with a release binary.")
		fmt.Println("Use --force to proceed anyway.")
		os.Exit(1)
	}

	fmt.Printf("Current: %s\n", formatVersion())
	fmt.Println("Checking for updates...")

	// When pinning a version, skip the "is there an update" gate.
	if pinVer == "" {
		info, err := updater.Check(ctx)
		if err != nil {
			fmt.Printf("Error checking for updates: %v\n", err)
			os.Exit(1)
		}
		if !info.UpdateAvailable {
			fmt.Printf("✓ Already up to date (%s).\n", info.Current)
			return
		}
		fmt.Printf("Update available: %s → %s\n", info.Current, info.Latest)
	} else {
		fmt.Printf("Installing pinned version: %s\n", pinVer)
	}

	if !assumeYes {
		if !confirm("Proceed with update?") {
			fmt.Println("Aborted.")
			return
		}
	}

	progress := func(s update.State) {
		switch s.Phase {
		case update.PhaseDownloading:
			fmt.Printf("\rDownloading... %5.1f%%", s.Progress)
		case update.PhaseVerifying:
			fmt.Println("\nVerifying checksum...")
		case update.PhaseInstalling:
			fmt.Println("Installing...")
		case update.PhaseRestarting:
			fmt.Println("Restarting service...")
		}
	}

	newVer, err := updater.Apply(ctx, update.Options{
		Version:  pinVer,
		Restart:  false, // CLI handles restart externally below
		Progress: progress,
	})
	if err != nil {
		fmt.Printf("\nUpdate failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Installed %s\n", newVer)

	if st := updater.State(); st.Error != "" {
		fmt.Printf("Warning: %s\n", st.Error)
	}

	if noRestart {
		fmt.Println("Restart skipped (--no-restart). Restart the service manually to use the new version.")
		return
	}

	// Restart the managed service from outside the gateway process.
	method, err := updater.Restarter.RestartService()
	if err != nil {
		fmt.Printf("Binary updated, but could not restart service: %v\n", err)
		fmt.Println("Restart lele manually to use the new version.")
		return
	}
	fmt.Printf("✓ Service restarted (%s)\n", method)
}

func runRollback(ctx context.Context, updater *update.Updater) {
	backup, err := updater.Installer.LatestBackup()
	if err != nil || backup == "" {
		fmt.Println("No backup available to roll back to.")
		os.Exit(1)
	}

	fmt.Printf("Rolling back to: %s\n", filepath.Base(backup))
	if !confirm("Proceed with rollback?") {
		fmt.Println("Aborted.")
		return
	}

	path, err := updater.Rollback(ctx)
	if err != nil {
		fmt.Printf("Rollback failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Restored %s\n", filepath.Base(path))

	method, err := updater.Restarter.RestartService()
	if err != nil {
		fmt.Printf("Rolled back, but could not restart service: %v\n", err)
		return
	}
	fmt.Printf("✓ Service restarted (%s)\n", method)
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func printUpdateHelp() {
	fmt.Println(`lele update - Update lele to the latest release

Usage:
  lele update                    Check and install the latest version (with confirmation)
  lele update --check            Only check if an update is available
  lele update --yes              Install without confirmation
  lele update --version vX.Y.Z   Install a specific version
  lele update --rollback         Restore the previous binary from backup
  lele update --no-restart       Do not restart the service after installing
  lele update --force            Allow updating a dev/local build

Options:
  -c, --check       Check only, do not install
  -y, --yes         Assume yes to prompts
  -v, --version     Install a specific version
  -f, --force       Force update of dev builds
      --no-restart  Skip service restart
      --rollback    Roll back to the previous version
  -h, --help        Show this help`)
}
