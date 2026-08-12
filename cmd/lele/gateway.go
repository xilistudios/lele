package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/devices"
	"github.com/xilistudios/lele/pkg/heartbeat"
	"github.com/xilistudios/lele/pkg/lockfile"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/server"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/update"
	"github.com/xilistudios/lele/pkg/voice"
)

// gatewayOut is the destination for human-readable gateway output. In normal
// (non-desktop) mode it defaults to os.Stdout. In desktop mode it is switched
// to os.Stderr so stdout stays reserved for machine-readable LELE_READY and
// LELE_ERROR lines.
var gatewayOut io.Writer = os.Stdout

// parseGatewayFlags extracts gateway flags from raw args.
// Returns desktop mode (--desktop or LELE_DESKTOP=1 env), port override
// (--port N; -1 means "not specified"), and debug flag.
func parseGatewayFlags(args []string) (desktop bool, port int, debug bool) {
	port = -1
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--desktop":
			desktop = true
		case "--port":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					port = n
				}
				i++
			}
		case "--debug", "-d":
			debug = true
		}
	}
	if os.Getenv("LELE_DESKTOP") == "1" {
		desktop = true
	}
	return desktop, port, debug
}

// emitDesktopError writes a machine-readable error line to stdout. It is only
// used in desktop mode; the JSON is built with encoding/json so the error
// field is properly escaped.
func emitDesktopError(code string, extra map[string]interface{}) {
	payload := make(map[string]interface{}, len(extra)+1)
	for k, v := range extra {
		payload[k] = v
	}
	payload["code"] = code
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stdout, "LELE_ERROR {\"code\":%q}\n", code)
		return
	}
	fmt.Fprintf(os.Stdout, "LELE_ERROR %s\n", data)
}

func gatewayCmd() {
	desktop, portOverride, debug := parseGatewayFlags(os.Args[2:])
	if debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Fprintln(gatewayOut, "🔍 Debug mode enabled")
	}
	if desktop {
		gatewayOut = os.Stderr
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(gatewayOut, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Desktop mode serves the web UI and API exclusively through the native
	// channel, so it must be enabled regardless of the user's config.
	if desktop {
		cfg.Channels.Native.Enabled = true
	}

	setupFileLogging(cfg)

	// In desktop mode a single gateway instance must be running; a lockfile
	// guards against a second instance being launched while the first lives.
	if desktop {
		lockPath := filepath.Join(getLeleDir(), "gateway.lock")
		lock, err := lockfile.Acquire(lockPath)
		if err != nil {
			var arErr *lockfile.AlreadyRunningError
			if errors.As(err, &arErr) {
				emitDesktopError("already_running", map[string]interface{}{"pid": arErr.PID})
				os.Exit(1)
			}
			emitDesktopError("lock_failed", map[string]interface{}{"error": err.Error()})
			os.Exit(1)
		}
		defer lock.Release()
	}

	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	fmt.Fprintln(gatewayOut, "\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo := startupInfo["tools"].(map[string]interface{})
	skillsInfo := startupInfo["skills"].(map[string]interface{})
	fmt.Fprintf(gatewayOut, "  • Tools: %d loaded\n", toolsInfo["count"])
	fmt.Fprintf(gatewayOut, "  • Skills: %d/%d available\n",
		skillsInfo["available"],
		skillsInfo["total"])

	approvalManager := channels.NewApprovalManager()
	agentLoop.SetApprovalManager(approvalManager)

	execTimeout := time.Duration(cfg.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	cronService := setupCronTool(agentLoop.GetProvidable(), agentLoop, msgBus, cfg.WorkspacePath(), cfg.Agents.Defaults.RestrictToWorkspace, execTimeout, cfg)

	// Wire SQLite store into cron service for persistent storage.
	if s := agentLoop.Store(); s != nil {
		cronService.SetStore(s.Cron())
	}

	heartbeatService := heartbeat.NewHeartbeatService(
		cfg.WorkspacePath(),
		cfg.Heartbeat.Interval,
		cfg.Heartbeat.Enabled,
	)
	heartbeatService.SetBus(msgBus)
	heartbeatService.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		if channel == "" || chatID == "" {
			channel, chatID = "cli", "direct"
		}
		response, err := agentLoop.GetProvidable().ProcessHeartbeat(context.Background(), prompt, channel, chatID)
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("Heartbeat error: %v", err))
		}
		if response == "HEARTBEAT_OK" {
			return tools.SilentResult("Heartbeat OK")
		}
		return tools.SilentResult(response)
	})

	channelManager, err := channels.NewManager(cfg, msgBus, agentLoop.GetProvidable(), approvalManager)
	if err != nil {
		fmt.Fprintf(gatewayOut, "Error creating channel manager: %v\n", err)
		os.Exit(1)
	}

	agentLoop.SetChannelManager(channelManager)

	// Wire the SQLite KV store into the Telegram channel so the last
	// processed update offset is persisted there instead of a flat file.
	if s := agentLoop.Store(); s != nil {
		if tc, ok := channelManager.GetChannel("telegram"); ok {
			if tgc, ok := tc.(*channels.TelegramChannel); ok {
				tgc.SetKVRepo(s.KV())
			}
		}
		// Wire SQLite store into native channel for client persistence.
		channelManager.SetNativeClientStore(s.NativeClients())
	}

	var transcriber *voice.GroqTranscriber
	if cfg.Providers.Groq.APIKey != "" {
		transcriber = voice.NewGroqTranscriber(cfg.Providers.Groq.APIKey)
	}

	if transcriber != nil {
		if telegramChannel, ok := channelManager.GetChannel("telegram"); ok {
			if tc, ok := telegramChannel.(*channels.TelegramChannel); ok {
				tc.SetTranscriber(transcriber)
			}
		}
		if discordChannel, ok := channelManager.GetChannel("discord"); ok {
			if dc, ok := discordChannel.(*channels.DiscordChannel); ok {
				dc.SetTranscriber(transcriber)
			}
		}
		if slackChannel, ok := channelManager.GetChannel("slack"); ok {
			if sc, ok := slackChannel.(*channels.SlackChannel); ok {
				sc.SetTranscriber(transcriber)
			}
		}
		if onebotChannel, ok := channelManager.GetChannel("onebot"); ok {
			if oc, ok := onebotChannel.(*channels.OneBotChannel); ok {
				oc.SetTranscriber(transcriber)
			}
		}
	}

	enabledChannels := channelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Fprintf(gatewayOut, "✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Fprintln(gatewayOut, "⚠ Warning: No channels enabled")
	}

	// --- Unified Server Setup ---
	serverHost := cfg.EffectiveServerHost()
	serverPort := cfg.EffectiveServerPort()

	if desktop {
		// In desktop mode the server must only be reachable from the local
		// machine (the Tauri shell), regardless of the configured host.
		serverHost = "127.0.0.1"
		if portOverride >= 0 {
			serverPort = portOverride
		} else {
			// Dynamic port by default so multiple desktop sessions never clash.
			serverPort = 0
		}
	} else if portOverride >= 0 {
		serverPort = portOverride
	}

	srv := server.New(&server.Config{
		Host: serverHost,
		Port: serverPort,
	})

	// Register health endpoints
	srv.RegisterHealth()

	// Register web UI (SPA)
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		logger.WarnC("server", "Web UI assets not available (build frontend with 'make build')")
	} else {
		srv.RegisterWebUI(http.FS(distFS))
	}

	// Register native channel API routes
	if nativeCh, ok := channelManager.GetChannel("native"); ok {
		if nc, ok := nativeCh.(*channels.NativeChannel); ok {
			nc.RegisterRoutes(srv.Mux())
			// Expose the cron service through the API so the Web UI and TUI
			// can view and manage scheduled jobs.
			nc.SetCronService(cronService)
			// Expose the keyring service so the Web UI can manage secrets.
			// Uses the same instance as the agent tool for a shared audit log.
			if ks := agentLoop.KeyringService(); ks != nil {
				nc.SetKeyringService(ks)
			}
			// Expose the self-update service so the Web UI can check for and
			// apply updates. Backups live under <leleDir>/backups.
			channels.SetBuildInfo(version, gitCommit, buildTime)
			updateBackupDir := filepath.Join(getLeleDir(), "backups")
			updater := update.NewUpdater(cfg.Updates.Repo, updateBackupDir, version)
			nc.SetUpdateService(updater)
			// Set up a reload callback so config changes via the API trigger
			// an immediate runtime reload (not waiting for fsnotify/kqueue).
			nc.SetReloadConfig(func() error {
				updated, err := config.LoadConfig(getConfigPath())
				if err != nil {
					return fmt.Errorf("failed to load config: %w", err)
				}
				agentLoop.ReloadRegistry(updated)
				if err := channelManager.ReloadConfig(updated); err != nil {
					return fmt.Errorf("failed to reload channels: %w", err)
				}
				heartbeatService.UpdateConfig(updated.Heartbeat.Interval, updated.Heartbeat.Enabled)
				return nil
			})
			// In desktop mode the Tauri shell authenticates with a fixed
			// trusted client. Register it so desktop auto-auth works.
			if desktop {
				token := os.Getenv("LELE_DESKTOP_TOKEN")
				if token != "" {
					refresh := os.Getenv("LELE_DESKTOP_REFRESH")
					if refresh == "" {
						sum := sha256.Sum256([]byte(token + ":refresh"))
						refresh = hex.EncodeToString(sum[:])
					}
					if err := nc.RegisterDesktopClient(token, refresh); err != nil {
						logger.ErrorCF("native", "Failed to register desktop client", map[string]interface{}{"error": err.Error()})
					} else {
						logger.InfoCF("native", "Desktop client registered", map[string]interface{}{"client_id": channels.DesktopClientID})
					}
				} else {
					logger.WarnC("native", "Desktop mode enabled but LELE_DESKTOP_TOKEN not set; desktop auto-auth disabled")
				}
			}
		}
	}

	// Register LINE webhook
	if lineCh, ok := channelManager.GetChannel("line"); ok {
		if lc, ok := lineCh.(*channels.LINEChannel); ok {
			lc.RegisterWebhook(srv.Mux())
		}
	}

	// Start unified server immediately so the Web UI and WebSocket endpoint
	// are available while channels initialize (some channels block in Start).
	// We bind the listener first so we can report the actual (dynamic) address.
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", serverHost, serverPort))
	if err != nil {
		if desktop {
			emitDesktopError("bind_failed", map[string]interface{}{"error": err.Error()})
			os.Exit(1)
		}
		fmt.Fprintf(gatewayOut, "Error binding server: %v\n", err)
		os.Exit(1)
	}
	actualAddr := ln.Addr().String()

	fmt.Fprintf(gatewayOut, "✓ Unified server starting on %s\n", actualAddr)
	fmt.Fprintln(gatewayOut, "  • Web UI:      /")
	fmt.Fprintln(gatewayOut, "  • API:         /api/v1/*")
	fmt.Fprintln(gatewayOut, "  • Health:      /health, /ready")
	fmt.Fprintln(gatewayOut, "  • WebSocket:   /api/v1/ws")
	if lineCh, ok := channelManager.GetChannel("line"); ok {
		_ = lineCh
		fmt.Fprintln(gatewayOut, "  • LINE webhook /webhook/line")
	}
	fmt.Fprintln(gatewayOut, "Press Ctrl+C to stop")

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("server", "Unified server error", map[string]interface{}{"error": err.Error()})
		}
	}()
	if desktop {
		_, portStr, _ := net.SplitHostPort(actualAddr)
		port, _ := strconv.Atoi(portStr)
		fmt.Fprintf(os.Stdout, "LELE_READY {\"url\":\"http://%s\",\"port\":%d}\n", actualAddr, port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := cronService.Start(); err != nil {
		fmt.Fprintf(gatewayOut, "Error starting cron service: %v\n", err)
	}
	fmt.Fprintln(gatewayOut, "✓ Cron service started")

	if err := heartbeatService.Start(); err != nil {
		fmt.Fprintf(gatewayOut, "Error starting heartbeat service: %v\n", err)
	}
	fmt.Fprintln(gatewayOut, "✓ Heartbeat service started")

	stateManager := state.NewManager(cfg.WorkspacePath())
	deviceService := devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	deviceService.SetBus(msgBus)
	if err := deviceService.Start(ctx); err != nil {
		fmt.Fprintf(gatewayOut, "Error starting device service: %v\n", err)
	} else if cfg.Devices.Enabled {
		fmt.Fprintln(gatewayOut, "✓ Device event service started")
	}

	if err := channelManager.StartAll(ctx); err != nil {
		fmt.Fprintf(gatewayOut, "Error starting channels: %v\n", err)
	}

	configWatcher := config.NewConfigWatcher(getConfigPath())
	go func() {
		if err := configWatcher.Start(ctx, func(updated *config.Config) error {
			// Reload registry first to pick up new agents
			agentLoop.ReloadRegistry(updated)
			if err := channelManager.ReloadConfig(updated); err != nil {
				return err
			}
			heartbeatService.UpdateConfig(updated.Heartbeat.Interval, updated.Heartbeat.Enabled)
			deviceService.UpdateConfig(devices.Config{Enabled: updated.Devices.Enabled, MonitorUSB: updated.Devices.MonitorUSB})
			return nil
		}); err != nil {
			logger.ErrorCF("config", "Config watcher error", map[string]interface{}{"error": err.Error()})
		}
	}()

	go agentLoop.Run(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(gatewayOut, "\nShutting down...")
	cancel()
	configWatcher.Stop()
	srv.Stop(context.Background())
	deviceService.Stop()
	heartbeatService.Stop()
	cronService.Stop()
	agentLoop.Stop()
	channelManager.StopAll(ctx)
	fmt.Fprintln(gatewayOut, "✓ Gateway stopped")
}

func setupCronTool(executor tools.JobExecutor, al *agent.AgentLoop, msgBus *bus.MessageBus, workspace string, restrict bool, execTimeout time.Duration, config *config.Config) *cron.CronService {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath, nil)

	cronTool := tools.NewCronTool(cronService, executor, msgBus, workspace, restrict, execTimeout, config)
	al.RegisterTool(cronTool)

	cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		result := cronTool.ExecuteJob(context.Background(), job)
		return result, nil
	})

	return cronService
}
