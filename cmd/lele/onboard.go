package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Add your API key to", configPath)
	fmt.Println("     Get one at: https://openrouter.ai/keys")
	fmt.Println("  2. Chat: lele agent -m \"Hello!\"")
}
