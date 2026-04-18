package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/i18n"
)

func clientCmd() {
	if len(os.Args) < 3 {
		clientHelp()
		return
	}

	subcommand := os.Args[2]

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	cfg, err := loadConfig()
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.common.errorLoadingConfig", err))
		os.Exit(1)
	}

	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.onboard.errorCreatingAuthManager", err))
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
			fmt.Println(i18n.T("cli.client.usageRemove"))
			return
		}
		clientRemoveCmd(authMgr, os.Args[3])
	case "status":
		clientStatusCmd(authMgr, cfg)
	default:
		fmt.Println(i18n.TPrintf("cli.common.unknownSubcommand", "client", subcommand))
		clientHelp()
	}
}

func clientHelp() {
	fmt.Println(i18n.T("cli.client.help.title"))
	fmt.Println(i18n.T("cli.client.help.pin"))
	fmt.Println(i18n.T("cli.client.help.list"))
	fmt.Println(i18n.T("cli.client.help.pending"))
	fmt.Println(i18n.T("cli.client.help.remove"))
	fmt.Println(i18n.T("cli.client.help.status"))
	fmt.Println()
	fmt.Println(i18n.T("cli.client.help.examples"))
	fmt.Println(i18n.T("cli.client.help.examplePin"))
	fmt.Println(i18n.T("cli.client.help.exampleList"))
	fmt.Println(i18n.T("cli.client.help.examplePending"))
	fmt.Println(i18n.T("cli.client.help.exampleRemove"))
	fmt.Println(i18n.T("cli.client.help.exampleStatus"))
}

func clientPinCmd(authMgr *channels.AuthManager) {
	deviceName := ""
	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device", "-d":
			if i+1 < len(args) {
				deviceName = args[i+1]
				i++
			}
		}
	}

	pending, err := authMgr.GeneratePIN(deviceName)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.client.errorGeneratingPIN", err))
		os.Exit(1)
	}

	fmt.Println(i18n.TPrintf("cli.client.pinGeneratedTitle", logo))
	fmt.Println(i18n.T("cli.client.pinSeparator"))
	fmt.Println(i18n.TPrintf("cli.client.pinLabel", pending.PIN))
	fmt.Println(i18n.TPrintf("cli.client.pinExpires", pending.Expires.Format("2006-01-02 15:04:05")))
	fmt.Println()
	fmt.Println(i18n.T("cli.client.enterPINToPair"))
}

func clientListCmd(authMgr *channels.AuthManager) {
	clients := authMgr.ListClients()

	if len(clients) == 0 {
		fmt.Println(i18n.T("cli.client.noPairedClients"))
		return
	}

	fmt.Println(i18n.TPrintf("cli.client.pairedClientsTitle", logo, len(clients)))
	fmt.Println(i18n.T("cli.client.pairedClientsSeparator"))
	for _, client := range clients {
		status := i18n.T("cli.client.clientStatusActive")
		if client.Expires.Before(time.Now()) {
			status = i18n.T("cli.client.clientStatusExpired")
		}

		fmt.Println(i18n.TPrintf("cli.client.clientID", client.ClientID))
		fmt.Println(i18n.TPrintf("cli.client.clientDevice", client.DeviceName))
		fmt.Println(i18n.TPrintf("cli.client.clientStatus", status))
		fmt.Println(i18n.TPrintf("cli.client.clientCreated", client.Created.Format("2006-01-02 15:04")))
		fmt.Println(i18n.TPrintf("cli.client.clientExpires", client.Expires.Format("2006-01-02 15:04")))
		fmt.Println(i18n.TPrintf("cli.client.clientLastSeen", client.LastSeen.Format("2006-01-02 15:04")))
	}
	fmt.Println()
}

func clientPendingCmd(authMgr *channels.AuthManager) {
	pins := authMgr.GetPendingPINs()

	if len(pins) == 0 {
		fmt.Println(i18n.T("cli.client.noPendingPairing"))
		return
	}

	fmt.Println(i18n.TPrintf("cli.client.pendingPairingTitle", logo, len(pins)))
	fmt.Println(i18n.T("cli.client.pendingPairingSeparator"))
	for _, pending := range pins {
		remaining := time.Until(pending.Expires)
		status := i18n.T("cli.client.pendingStatusValid")
		if remaining <= 0 {
			status = i18n.T("cli.client.pendingStatusExpired")
		}

		fmt.Println(i18n.TPrintf("cli.client.pendingPIN", pending.PIN))
		fmt.Println(i18n.TPrintf("cli.client.pendingDevice", pending.DeviceName))
		fmt.Println(i18n.TPrintf("cli.client.pendingStatus", status))
		fmt.Println(i18n.TPrintf("cli.client.pendingRemaining", remaining.Round(time.Second)))
	}
	fmt.Println()
}

func clientRemoveCmd(authMgr *channels.AuthManager, clientID string) {
	if err := authMgr.RemoveClient(clientID); err != nil {
		fmt.Println(i18n.TPrintf("cli.client.errorRemovingClient", err))
		os.Exit(1)
	}

	fmt.Println(i18n.TPrintf("cli.client.clientRemoved", clientID))
}

func clientStatusCmd(authMgr *channels.AuthManager, cfg *config.Config) {
	clients := authMgr.ListClients()
	pins := authMgr.GetPendingPINs()

	fmt.Println(i18n.TPrintf("cli.client.clientChannelStatusTitle", logo))
	fmt.Println(i18n.T("cli.client.clientChannelStatusSeparator"))

	nativeCfg := cfg.Channels.Native
	fmt.Println(i18n.TPrintf("cli.client.clientEnabled", nativeCfg.Enabled))
	if nativeCfg.Enabled {
		fmt.Println(i18n.TPrintf("cli.client.clientHost", nativeCfg.Host))
		fmt.Println(i18n.TPrintf("cli.client.clientPort", nativeCfg.Port))
	}
	fmt.Println(i18n.TPrintf("cli.client.clientMaxClients", nativeCfg.MaxClients))
	fmt.Println(i18n.TPrintf("cli.client.clientTokenExpiry", nativeCfg.TokenExpiryDays))
	fmt.Println(i18n.TPrintf("cli.client.clientPINExpiry", nativeCfg.PinExpiryMinutes))

	fmt.Println()
	activeClients := 0
	for _, c := range clients {
		if c.Expires.After(time.Now()) {
			activeClients++
		}
	}
	fmt.Println(i18n.TPrintf("cli.client.clientPairedCount", len(clients), activeClients))

	fmt.Println(i18n.TPrintf("cli.client.clientPendingPINs", len(pins)))

	if nativeCfg.Enabled {
		fmt.Println()
		fmt.Println(i18n.TPrintf("cli.client.clientConnectWS", nativeCfg.Host, nativeCfg.Port))
		fmt.Println(i18n.TPrintf("cli.client.clientConnectREST", nativeCfg.Host, nativeCfg.Port))
	}
	fmt.Println()
}
