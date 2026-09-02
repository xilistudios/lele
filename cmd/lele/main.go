package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/skills"
	appversion "github.com/xilistudios/lele/pkg/version"
)

func main() {
	// Make the ldflags-injected version the single source of truth for every
	// display path (TUI sidebar, /status, API). Without this, consumers fall
	// back to build-info, which Go stamps with "+dirty" whenever the tree was
	// modified during the build — released binaries showed "0.7.10-dirty".
	appversion.Set(version)

	configDir, remaining := parseConfigDirFlag(os.Args[1:])
	if configDir != "" {
		os.Setenv("LELE_CONFIG_DIR", configDir)
	}

	sessionID, remaining := parseSessionFlag(remaining)
	os.Args = append([]string{os.Args[0]}, remaining...)

	if len(remaining) < 1 {
		if sessionID != "" {
			tuiCmd(sessionID)
			return
		}
		printHelp()
		os.Exit(1)
	}

	command := remaining[0]

	switch command {
	case "onboard":
		onboard()
	case "agent":
		agentCmd()
	case "gateway":
		gatewayCmd()
	case "status":
		statusCmd()
	case "migrate":
		migrateCmd()
	case "migrate-storage":
		migrateStorageCmd()
	case "auth":
		authCmd()
	case "cron":
		cronCmd()
	case "web":
		webCmd()
	case "skills":
		if len(os.Args) < 3 {
			skillsHelp()
			return
		}

		subcommand := os.Args[2]

		cfg, err := loadConfig()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		workspace := cfg.WorkspacePath()
		installer := skills.NewSkillInstaller(workspace)
		globalDir := filepath.Dir(getConfigPath())
		globalSkillsDir := filepath.Join(globalDir, "skills")
		builtinSkillsDir := filepath.Join(globalDir, "lele", "skills")
		skillsLoader := skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir)

		switch subcommand {
		case "list":
			skillsListCmd(skillsLoader)
		case "install":
			skillsInstallCmd(installer)
		case "remove", "uninstall":
			if len(os.Args) < 4 {
				fmt.Println("Usage: lele skills remove <skill-name>")
				return
			}
			skillsRemoveCmd(installer, os.Args[3])
		case "install-builtin":
			skillsInstallBuiltinCmd(workspace)
		case "list-builtin":
			skillsListBuiltinCmd()
		case "search":
			skillsSearchCmd(installer)
		case "show":
			if len(os.Args) < 4 {
				fmt.Println("Usage: lele skills show <skill-name>")
				return
			}
			skillsShowCmd(skillsLoader, os.Args[3])
		default:
			fmt.Printf("Unknown skills command: %s\n", subcommand)
			skillsHelp()
		}
	case "client":
		clientCmd()
	case "update":
		updateCmd()
	case "tui":
		tuiCmd(sessionID)
	case "version", "--version", "-v":
		printVersion()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printHelp()
		os.Exit(1)
	}
}

func parseSessionFlag(args []string) (sessionID string, remaining []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--session=") {
			sessionID = strings.TrimPrefix(arg, "--session=")
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+1:]...)
			return sessionID, remaining
		}
		if strings.HasPrefix(arg, "-s=") {
			sessionID = strings.TrimPrefix(arg, "-s=")
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+1:]...)
			return sessionID, remaining
		}
		if arg == "-s" || arg == "--session" {
			if i+1 < len(args) {
				sessionID = args[i+1]
				remaining = append(remaining, args[:i]...)
				remaining = append(remaining, args[i+2:]...)
				return sessionID, remaining
			}
		}
	}
	return "", args
}

func parseConfigDirFlag(args []string) (configDir string, remaining []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--config-dir=") {
			configDir = strings.TrimPrefix(arg, "--config-dir=")
			validateConfigDir(configDir)
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+1:]...)
			return configDir, remaining
		}
		if strings.HasPrefix(arg, "-c=") {
			configDir = strings.TrimPrefix(arg, "-c=")
			validateConfigDir(configDir)
			remaining = append(remaining, args[:i]...)
			remaining = append(remaining, args[i+1:]...)
			return configDir, remaining
		}
		if arg == "--config-dir" || arg == "-c" {
			if i+1 < len(args) {
				configDir = args[i+1]
				validateConfigDir(configDir)
				remaining = append(remaining, args[:i]...)
				remaining = append(remaining, args[i+2:]...)
				return configDir, remaining
			}
		}
	}
	return "", args
}

func validateConfigDir(configDir string) {
	if info, err := os.Stat(configDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: config directory does not exist: %s\n", configDir)
		os.Exit(1)
	} else if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: config path is not a directory: %s\n", configDir)
		os.Exit(1)
	}
}
