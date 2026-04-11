package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

func printHelp() {
	fmt.Printf("%s lele - Personal AI Assistant v%s\n\n", logo, version)
	fmt.Println("Usage: lele <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  onboard     Initialize lele configuration and workspace")
	fmt.Println("  agent       Interact with the agent directly")
	fmt.Println("  auth        Manage authentication (login, logout, status)")
	fmt.Println("  gateway     Start lele gateway")
	fmt.Println("  web         Start or stop the web app server")
	fmt.Println("  status      Show lele status")
	fmt.Println("  cron        Manage scheduled tasks")
	fmt.Println("  migrate     Migrate from OpenClaw to Lele")
	fmt.Println("  skills      Manage skills (install, list, remove)")
	fmt.Println("  client      Manage native channel clients (pair, list, remove)")
	fmt.Println("  version     Show version information")
}

func askYesNo(prompt string, defaultYes bool) bool {
	defaultHint := "y/N"
	if defaultYes {
		defaultHint = "Y/n"
	}
	fmt.Printf("%s (%s): ", prompt, defaultHint)

	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "" {
		return defaultYes
	}
	return response == "y" || response == "yes"
}

func askString(prompt string, defaultVal string) string {
	fmt.Printf("%s [%s]: ", prompt, defaultVal)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultVal
	}
	return response
}

func askInt(prompt string, defaultVal int) int {
	fmt.Printf("%s [%d]: ", prompt, defaultVal)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultVal
	}

	var result int
	fmt.Sscanf(response, "%d", &result)
	if result == 0 {
		return defaultVal
	}
	return result
}

func configureWebUI(cfg *config.Config, leleDir string) {
	fmt.Println("\n=== Web UI Configuration ===")

	cfg.Channels.Web.Enabled = true
	cfg.Channels.Web.Port = askInt("Web port", 3005)
	cfg.Channels.Web.Host = "0.0.0.0"

	fmt.Println("\n✓ Web UI enabled on port", cfg.Channels.Web.Port)

	fmt.Println("✓ Native channel auto-enabled (required for Web UI)")
	cfg.Channels.Native.Enabled = true

	if askYesNo("[Advanced] Configure native channel?", false) {
		configureNativeAdvanced(cfg)
	}
}

func configureNativeAdvanced(cfg *config.Config) {
	fmt.Println("\n=== Native Channel Configuration (Advanced) ===")

	cfg.Channels.Native.Host = askString("API host", "127.0.0.1")
	cfg.Channels.Native.Port = askInt("API port", 18793)
	cfg.Channels.Native.MaxClients = askInt("Max paired clients", 5)
	cfg.Channels.Native.TokenExpiryDays = askInt("Token expiry days", 30)

	fmt.Println("\n✓ Native channel configured")
}

func maybeGeneratePIN(cfg *config.Config, leleDir string) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo("Generate pairing PIN?", true) {
		return
	}

	deviceName := askString("Device name", "Desktop")

	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		fmt.Printf("Error creating auth manager: %v\n", err)
		return
	}

	pending, err := authMgr.GeneratePIN(deviceName)
	if err != nil {
		fmt.Printf("Error generating PIN: %v\n", err)
		return
	}

	fmt.Println("\n✓ Pairing PIN generated")
	fmt.Printf("  PIN:     %s\n", pending.PIN)
	fmt.Printf("  Expires: %s (%d minutes)\n",
		pending.Expires.Format("15:04:05"),
		cfg.Channels.Native.PinExpiryMinutes)
}

func maybeStartServices(cfg *config.Config) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo("Start services now?", true) {
		fmt.Println("\nTo start services manually:")
		fmt.Println("  lele gateway")
		fmt.Println("  lele web start")
		return
	}

	fmt.Println("\n[+] Starting gateway...")
	gatewayCmd := exec.Command("lele", "gateway")
	gatewayCmd.Start()

	time.Sleep(1 * time.Second)

	fmt.Println("[+] Starting web server...")
	webCmd := exec.Command("lele", "web", "start",
		"--port", fmt.Sprintf("%d", cfg.Channels.Web.Port))
	webCmd.Start()

	time.Sleep(500 * time.Millisecond)

	fmt.Println("\n✓ Services started")
	fmt.Printf("\nOpen http://127.0.0.1:%d in your browser\n", cfg.Channels.Web.Port)
	fmt.Println("Enter the PIN to connect your device.")
}

func onboard() {
	configPath := getConfigPath()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config already exists at %s\n", configPath)
		fmt.Print("Overwrite? (y/n): ")
		var response string
		fmt.Scanln(&response)
		if response != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	cfg := config.DefaultConfig()

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	if askYesNo("\nEnable Web UI?", true) {
		configureWebUI(cfg, leleDir)
		maybeGeneratePIN(cfg, leleDir)
		maybeStartServices(cfg)
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	workspace := cfg.WorkspacePath()
	createWorkspaceTemplates(workspace)

	logsPath := cfg.LogsPath()
	if err := os.MkdirAll(logsPath, 0755); err != nil {
		fmt.Printf("Warning: could not create logs directory: %v\n", err)
	} else {
		currentDate := time.Now().Format("2006-01-02")
		infoLog := filepath.Join(logsPath, fmt.Sprintf("info-%s.log", currentDate))
		errorsLog := filepath.Join(logsPath, fmt.Sprintf("errors-%s.log", currentDate))

		if _, err := os.Create(infoLog); err != nil {
			fmt.Printf("Warning: could not create info log file: %v\n", err)
		}
		if _, err := os.Create(errorsLog); err != nil {
			fmt.Printf("Warning: could not create errors log file: %v\n", err)
		}
	}

	fmt.Printf("%s lele is ready!\n", logo)
}
