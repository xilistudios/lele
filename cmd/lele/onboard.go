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
	"github.com/xilistudios/lele/pkg/i18n"
)

func printHelp() {
	fmt.Println(i18n.TWithData("cli.help.title", map[string]interface{}{
		"logo":    logo,
		"version": version,
	}))
	fmt.Println()
	fmt.Println(i18n.T("cli.help.usage"))
	fmt.Println()
	fmt.Println(i18n.T("cli.help.commands"))
	fmt.Println("  onboard     " + i18n.T("cli.help.onboard"))
	fmt.Println("  agent       " + i18n.T("cli.help.agent"))
	fmt.Println("  auth        " + i18n.T("cli.help.auth"))
	fmt.Println("  gateway     " + i18n.T("cli.help.gateway"))
	fmt.Println("  web         " + i18n.T("cli.help.web"))
	fmt.Println("  status      " + i18n.T("cli.help.status"))
	fmt.Println("  cron        " + i18n.T("cli.help.cron"))
	fmt.Println("  migrate     " + i18n.T("cli.help.migrate"))
	fmt.Println("  skills      " + i18n.T("cli.help.skills"))
	fmt.Println("  client      " + i18n.T("cli.help.client"))
	fmt.Println("  version     " + i18n.T("cli.help.version"))
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
	fmt.Println(i18n.T("cli.onboard.webUIConfiguration"))

	cfg.Channels.Web.Enabled = true
	cfg.Channels.Web.Port = askInt(i18n.T("cli.onboard.webPort"), 3005)
	cfg.Channels.Web.Host = "0.0.0.0"

	fmt.Println(i18n.T("cli.onboard.webUIEnabled"), cfg.Channels.Web.Port)

	fmt.Println(i18n.T("cli.onboard.nativeChannelAutoEnabled"))
	cfg.Channels.Native.Enabled = true

	if askYesNo(i18n.T("cli.onboard.configureNativeAdvanced"), false) {
		configureNativeAdvanced(cfg)
	}
}

func configureNativeAdvanced(cfg *config.Config) {
	fmt.Println(i18n.T("cli.onboard.nativeChannelConfiguration"))

	cfg.Channels.Native.Host = askString(i18n.T("cli.onboard.apiHost"), "127.0.0.1")
	cfg.Channels.Native.Port = askInt(i18n.T("cli.onboard.apiPort"), 18793)
	cfg.Channels.Native.MaxClients = askInt(i18n.T("cli.onboard.maxPairedClients"), 5)
	cfg.Channels.Native.TokenExpiryDays = askInt(i18n.T("cli.onboard.tokenExpiryDays"), 30)

	fmt.Println(i18n.T("cli.onboard.nativeChannelConfigured"))
}

func maybeGeneratePIN(cfg *config.Config, leleDir string) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo(i18n.T("cli.onboard.generatePairingPIN"), true) {
		return
	}

	deviceName := askString(i18n.T("cli.onboard.deviceName"), "Desktop")

	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.onboard.errorCreatingAuthManager", err))
		return
	}

	pending, err := authMgr.GeneratePIN(deviceName)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.onboard.errorGeneratingPIN", err))
		return
	}

	fmt.Println(i18n.T("cli.onboard.pairingPINGenerated"))
	fmt.Println(i18n.TPrintf("cli.onboard.pinLabel", pending.PIN))
	fmt.Println(i18n.TPrintf("cli.onboard.pinExpires",
		pending.Expires.Format("15:04:05"),
		cfg.Channels.Native.PinExpiryMinutes))
}

func maybeStartServices(cfg *config.Config) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo(i18n.T("cli.onboard.startServicesNow"), true) {
		fmt.Println(i18n.T("cli.onboard.manualStartInstructions"))
		fmt.Println(i18n.T("cli.onboard.manualStartGateway"))
		fmt.Println(i18n.T("cli.onboard.manualStartWeb"))
		return
	}

	fmt.Println(i18n.T("cli.onboard.startingGateway"))
	gatewayCmd := exec.Command("lele", "gateway")
	gatewayCmd.Start()

	time.Sleep(1 * time.Second)

	fmt.Println(i18n.T("cli.onboard.startingWebServer"))
	webCmd := exec.Command("lele", "web", "start",
		"--port", fmt.Sprintf("%d", cfg.Channels.Web.Port))
	webCmd.Start()

	time.Sleep(500 * time.Millisecond)

	fmt.Println(i18n.T("cli.onboard.servicesStarted"))
	fmt.Println(i18n.TPrintf("cli.onboard.openBrowser", cfg.Channels.Web.Port))
	fmt.Println(i18n.T("cli.onboard.enterPINToConnect"))
}

func onboard() {
	configPath := getConfigPath()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Print(i18n.TPrintf("cli.common.overwritePrompt", configPath))
		var response string
		fmt.Scanln(&response)
		if response != "y" {
			fmt.Println(i18n.T("cli.common.aborted"))
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
		fmt.Println(i18n.TPrintf("cli.onboard.errorSavingConfig", err))
		os.Exit(1)
	}

	workspace := cfg.WorkspacePath()
	createWorkspaceTemplates(workspace)

	logsPath := cfg.LogsPath()
	if err := os.MkdirAll(logsPath, 0755); err != nil {
		fmt.Println(i18n.TPrintf("cli.onboard.warningCreateLogsDir", err))
	} else {
		currentDate := time.Now().Format("2006-01-02")
		infoLog := filepath.Join(logsPath, fmt.Sprintf("info-%s.log", currentDate))
		errorsLog := filepath.Join(logsPath, fmt.Sprintf("errors-%s.log", currentDate))

		if _, err := os.Create(infoLog); err != nil {
			fmt.Println(i18n.TPrintf("cli.onboard.warningCreateInfoLog", err))
		}
		if _, err := os.Create(errorsLog); err != nil {
			fmt.Println(i18n.TPrintf("cli.onboard.warningCreateErrorsLog", err))
		}
	}

	fmt.Println(i18n.TPrintf("cli.onboard.ready", logo))
}
