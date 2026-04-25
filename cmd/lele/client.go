package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

// parseClientSubcommand extracts the subcommand for testability.
func parseClientSubcommand(args []string) (subcommand string) {
	if len(args) < 1 {
		return ""
	}
	return args[0]
}

func clientCmd() {
	if len(os.Args) < 3 {
		clientHelp()
		return
	}

	subcommand := parseClientSubcommand(os.Args[2:])

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		fmt.Printf("Error creating auth manager: %v\n", err)
		os.Exit(1)
	}

	switch subcommand {
	case "pin":
		clientPinCmd(authMgr)
	case "list":
		clientListCmd(authMgr)
	case "pending":
		clientPendingCmd(authMgr)
	case "remove":
		if len(os.Args) < 4 {
			fmt.Println("Usage: lele client remove <client_id>")
			return
		}
		clientRemoveCmd(authMgr, os.Args[3])
	case "status":
		clientStatusCmd(authMgr, cfg)
	default:
		fmt.Printf("Unknown client command: %s\n", subcommand)
		clientHelp()
	}
}

func clientHelp() {
	fmt.Println("\nClient commands:")
	fmt.Println("  pin         Generate a new pairing PIN")
	fmt.Println("  list        List all paired clients")
	fmt.Println("  pending     List pending pairing requests")
	fmt.Println("  remove      Remove a paired client")
	fmt.Println("  status      Show client channel status")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  lele client pin")
	fmt.Println("  lele client list")
	fmt.Println("  lele client pending")
	fmt.Println("  lele client remove <client_id>")
	fmt.Println("  lele client status")
}

// parseClientPinArgs extracts the pin argument parsing logic for testability.
func parseClientPinArgs(args []string) (deviceName string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device", "-d":
			if i+1 < len(args) {
				deviceName = args[i+1]
				i++
			}
		}
	}
	return deviceName
}

func clientPinCmd(authMgr *channels.AuthManager) {
	deviceName := parseClientPinArgs(os.Args[3:])

	pending, err := authMgr.GeneratePIN(deviceName)
	if err != nil {
		fmt.Printf("✗ Error generating PIN: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%s Pairing PIN Generated\n", logo)
	fmt.Println("------------------------")
	fmt.Printf("  PIN:     %s\n", pending.PIN)
	fmt.Printf("  Expires: %s\n", pending.Expires.Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Println("Enter this PIN in your native client to pair.")
}

func clientListCmd(authMgr *channels.AuthManager) {
	clients := authMgr.ListClients()

	if len(clients) == 0 {
		fmt.Println("No paired clients.")
		return
	}

	fmt.Printf("\n%s Paired Clients (%d)\n", logo, len(clients))
	fmt.Println("----------------------")
	for _, client := range clients {
		status := "✓ active"
		if client.Expires.Before(time.Now()) {
			status = "✗ expired"
		}

		fmt.Printf("\n  %s\n", client.ClientID)
		fmt.Printf("    Device:  %s\n", client.DeviceName)
		fmt.Printf("    Status:  %s\n", status)
		fmt.Printf("    Created: %s\n", client.Created.Format("2006-01-02 15:04"))
		fmt.Printf("    Expires: %s\n", client.Expires.Format("2006-01-02 15:04"))
		fmt.Printf("    Last:    %s\n", client.LastSeen.Format("2006-01-02 15:04"))
	}
	fmt.Println()
}

func clientPendingCmd(authMgr *channels.AuthManager) {
	pins := authMgr.GetPendingPINs()

	if len(pins) == 0 {
		fmt.Println("No pending pairing requests.")
		return
	}

	fmt.Printf("\n%s Pending Pairing Requests (%d)\n", logo, len(pins))
	fmt.Println("---------------------------------")
	for _, pending := range pins {
		remaining := time.Until(pending.Expires)
		status := "✓ valid"
		if remaining <= 0 {
			status = "✗ expired"
		}

		fmt.Printf("\n  PIN: %s\n", pending.PIN)
		fmt.Printf("    Device:    %s\n", pending.DeviceName)
		fmt.Printf("    Status:    %s\n", status)
		fmt.Printf("    Remaining: %v\n", remaining.Round(time.Second))
	}
	fmt.Println()
}

func clientRemoveCmd(authMgr *channels.AuthManager, clientID string) {
	if err := authMgr.RemoveClient(clientID); err != nil {
		fmt.Printf("✗ Error removing client: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Client '%s' removed\n", clientID)
}

func clientStatusCmd(authMgr *channels.AuthManager, cfg *config.Config) {
	clients := authMgr.ListClients()
	pins := authMgr.GetPendingPINs()

	fmt.Printf("\n%s Client Channel Status\n", logo)
	fmt.Println("-----------------------")

	nativeCfg := cfg.Channels.Native
	fmt.Printf("  Enabled:     %v\n", nativeCfg.Enabled)
	if nativeCfg.Enabled {
		fmt.Printf("  Host:        %s\n", nativeCfg.Host)
		fmt.Printf("  Port:        %d\n", nativeCfg.Port)
	}
	fmt.Printf("  Max Clients: %d\n", nativeCfg.MaxClients)
	fmt.Printf("  Token Expiry: %d days\n", nativeCfg.TokenExpiryDays)
	fmt.Printf("  PIN Expiry:   %d minutes\n", nativeCfg.PinExpiryMinutes)

	fmt.Println()
	fmt.Printf("  Paired Clients: %d", len(clients))
	activeClients := 0
	for _, c := range clients {
		if c.Expires.After(time.Now()) {
			activeClients++
		}
	}
	fmt.Printf(" (%d active)\n", activeClients)

	fmt.Printf("  Pending PINs:   %d\n", len(pins))

	if nativeCfg.Enabled {
		fmt.Println()
		fmt.Printf("  Connect: ws://%s:%d/api/v1/ws\n", nativeCfg.Host, nativeCfg.Port)
		fmt.Printf("  REST:    http://%s:%d/api/v1/\n", nativeCfg.Host, nativeCfg.Port)
	}
	fmt.Println()
}
