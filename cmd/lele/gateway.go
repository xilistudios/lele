package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/devices"
	"github.com/xilistudios/lele/pkg/heartbeat"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/server"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/voice"
)

func gatewayCmd() {
	args := os.Args[2:]
	for _, arg := range args {
		if arg == "--debug" || arg == "-d" {
			logger.SetLevel(logger.DEBUG)
			fmt.Println("🔍 Debug mode enabled")
			break
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.Logs.Enabled {
		logsPath := cfg.LogsPath()
		if err := logger.EnableMultiFileLogging(logsPath); err != nil {
			fmt.Printf("Warning: could not enable file logging: %v\n", err)
		} else {
			if cfg.Logs.MaxDays > 0 {
				if err := logger.CleanupOldLogs(cfg.Logs.MaxDays); err != nil {
					fmt.Printf("Warning: could not cleanup old logs: %v\n", err)
				}
			}
		}
	}

	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	fmt.Println("\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo := startupInfo["tools"].(map[string]interface{})
	skillsInfo := startupInfo["skills"].(map[string]interface{})
	fmt.Printf("  • Tools: %d loaded\n", toolsInfo["count"])
	fmt.Printf("  • Skills: %d/%d available\n",
		skillsInfo["available"],
		skillsInfo["total"])

	logger.InfoCF("agent", "Agent initialized",
		map[string]interface{}{
			"tools_count":      toolsInfo["count"],
			"skills_total":     skillsInfo["total"],
			"skills_available": skillsInfo["available"],
		})

	approvalManager := channels.NewApprovalManager()
	agentLoop.SetApprovalManager(approvalManager)

	execTimeout := time.Duration(cfg.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	cronService := setupCronTool(agentLoop, msgBus, cfg.WorkspacePath(), cfg.Agents.Defaults.RestrictToWorkspace, execTimeout, cfg)

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
		response, err := agentLoop.ProcessHeartbeat(context.Background(), prompt, channel, chatID)
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("Heartbeat error: %v", err))
		}
		if response == "HEARTBEAT_OK" {
			return tools.SilentResult("Heartbeat OK")
		}
		return tools.SilentResult(response)
	})

	channelManager, err := channels.NewManager(cfg, msgBus, agentLoop, approvalManager)
	if err != nil {
		fmt.Printf("Error creating channel manager: %v\n", err)
		os.Exit(1)
	}

	agentLoop.SetChannelManager(channelManager)

	var transcriber *voice.GroqTranscriber
	if cfg.Providers.Groq.APIKey != "" {
		transcriber = voice.NewGroqTranscriber(cfg.Providers.Groq.APIKey)
		logger.InfoC("voice", "Groq voice transcription enabled")
	}

	if transcriber != nil {
		if telegramChannel, ok := channelManager.GetChannel("telegram"); ok {
			if tc, ok := telegramChannel.(*channels.TelegramChannel); ok {
				tc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Telegram channel")
			}
		}
		if discordChannel, ok := channelManager.GetChannel("discord"); ok {
			if dc, ok := discordChannel.(*channels.DiscordChannel); ok {
				dc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Discord channel")
			}
		}
		if slackChannel, ok := channelManager.GetChannel("slack"); ok {
			if sc, ok := slackChannel.(*channels.SlackChannel); ok {
				sc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Slack channel")
			}
		}
		if onebotChannel, ok := channelManager.GetChannel("onebot"); ok {
			if oc, ok := onebotChannel.(*channels.OneBotChannel); ok {
				oc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to OneBot channel")
			}
		}
	}

	enabledChannels := channelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Printf("✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Println("⚠ Warning: No channels enabled")
	}

	// --- Unified Server Setup ---
	serverHost := cfg.EffectiveServerHost()
	serverPort := cfg.EffectiveServerPort()

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
		logger.InfoC("server", "Web UI registered")
	}

	// Register native channel API routes
	if nativeCh, ok := channelManager.GetChannel("native"); ok {
		if nc, ok := nativeCh.(*channels.NativeChannel); ok {
			nc.RegisterRoutes(srv.Mux())
			logger.InfoC("server", "Native channel API routes registered")
		}
	}

	// Register LINE webhook
	if lineCh, ok := channelManager.GetChannel("line"); ok {
		if lc, ok := lineCh.(*channels.LINEChannel); ok {
			lc.RegisterWebhook(srv.Mux())
			logger.InfoC("server", "LINE webhook registered")
		}
	}

	fmt.Printf("✓ Unified server starting on %s:%d\n", serverHost, serverPort)
	fmt.Println("  • Web UI:      /")
	fmt.Println("  • API:         /api/v1/*")
	fmt.Println("  • Health:      /health, /ready")
	fmt.Println("  • WebSocket:   /api/v1/ws")
	if lineCh, ok := channelManager.GetChannel("line"); ok {
		_ = lineCh
		fmt.Println("  • LINE webhook /webhook/line")
	}
	fmt.Println("Press Ctrl+C to stop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := cronService.Start(); err != nil {
		fmt.Printf("Error starting cron service: %v\n", err)
	}
	fmt.Println("✓ Cron service started")

	if err := heartbeatService.Start(); err != nil {
		fmt.Printf("Error starting heartbeat service: %v\n", err)
	}
	fmt.Println("✓ Heartbeat service started")

	stateManager := state.NewManager(cfg.WorkspacePath())
	deviceService := devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	deviceService.SetBus(msgBus)
	if err := deviceService.Start(ctx); err != nil {
		fmt.Printf("Error starting device service: %v\n", err)
	} else if cfg.Devices.Enabled {
		fmt.Println("✓ Device event service started")
	}

	if err := channelManager.StartAll(ctx); err != nil {
		fmt.Printf("Error starting channels: %v\n", err)
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

	// Start unified server in goroutine
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("server", "Unified server error", map[string]interface{}{"error": err.Error()})
		}
	}()

	go agentLoop.Run(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	<-sigChan

	fmt.Println("\nShutting down...")
	cancel()
	configWatcher.Stop()
	srv.Stop(context.Background())
	deviceService.Stop()
	heartbeatService.Stop()
	cronService.Stop()
	agentLoop.Stop()
	channelManager.StopAll(ctx)
	fmt.Println("✓ Gateway stopped")
}

func setupCronTool(agentLoop *agent.AgentLoop, msgBus *bus.MessageBus, workspace string, restrict bool, execTimeout time.Duration, config *config.Config) *cron.CronService {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath, nil)

	cronTool := tools.NewCronTool(cronService, agentLoop, msgBus, workspace, restrict, execTimeout, config)
	agentLoop.RegisterTool(cronTool)

	cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		result := cronTool.ExecuteJob(context.Background(), job)
		return result, nil
	})

	return cronService
}
