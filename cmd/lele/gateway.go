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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/devices"
	"github.com/xilistudios/lele/pkg/durable"
	"github.com/xilistudios/lele/pkg/heartbeat"
	"github.com/xilistudios/lele/pkg/lockfile"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/server"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/store"
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

	// Root context for every long-running gateway service. It is cancelled
	// only AFTER the graceful teardown has run (see runGracefulShutdown):
	// cancelling it before the hooks fire is what used to kill in-flight turns
	// mid-request, because the agent loop derives its work from this context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// In desktop mode a single gateway instance must be running; a lockfile
	// guards against a second instance being launched while the first lives.
	// The lock is released by the "lock-release" shutdown hook (last hook to
	// run), not by a defer, so a restarting child never sees a stale holder.
	var instanceLock *lockfile.Lock
	if desktop {
		lockPath := filepath.Join(getLeleDir(), "gateway.lock")
		lock, err := acquireInstanceLock(lockPath)
		if err != nil {
			var arErr *lockfile.AlreadyRunningError
			if errors.As(err, &arErr) {
				emitDesktopError("already_running", map[string]interface{}{"pid": arErr.PID})
				os.Exit(1)
			}
			emitDesktopError("lock_failed", map[string]interface{}{"error": err.Error()})
			os.Exit(1)
		}
		instanceLock = lock
	}

	// Single teardown plan for the whole process. Both the signal path and the
	// self-update restart path drive it through runGracefulShutdown, so the
	// gateway can never be half-torn-down by two competing stop sequences. The
	// hooks themselves are registered further below, once every service they
	// stop exists.
	coord := update.NewShutdownCoordinator(update.DefaultShutdownBudget)

	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	fmt.Fprintln(gatewayOut, "\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo, _ := startupInfo["tools"].(map[string]interface{})
	skillsInfo, _ := startupInfo["skills"].(map[string]interface{})
	if toolsInfo == nil {
		toolsInfo = map[string]interface{}{"count": 0}
	}
	if skillsInfo == nil {
		skillsInfo = map[string]interface{}{"available": 0, "total": 0}
	}
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

	// Durable inbound spool (feature-flagged). Built here, after the channel
	// manager exists and before StartAll, because the two setters it calls
	// must run before channels accept traffic and before the agent loop
	// starts consuming. Returns nil when the flag is off or there is no
	// store, in which case every di.* call below is a no-op and the gateway
	// behaves exactly as it did before this feature existed.
	var spoolRepo *store.SpoolRepo
	if s := agentLoop.Store(); s != nil {
		spoolRepo = s.Spool()
	}
	di := setupDurableInbound(cfg.Session.DurableInboundEnabled(), spoolRepo, msgBus, agentLoop, channelManager)

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
			// A self-update restart terminates this process from inside the
			// native channel (self-exec path). Route it through the very same
			// teardown the signal path uses: sessions get saved, the instance
			// lock is released, and the replacement child (LELE_RESTART_CHILD=1)
			// can take the lock over as soon as we let go of it.
			if updater.Restarter != nil {
				updater.Restarter.OnRestart = func(string) {
					runGracefulShutdown(coord, agentLoop, cancel)
				}
			}
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

	// Replay everything the previous process left in the spool BEFORE the
	// channels accept any traffic. Ordering matters twice over: the restored
	// messages must reach the loop ahead of new ones, and once channels are
	// live every message they spool is a pending row that this pass would
	// happily replay as if it were left over from a dead process. Drain on a
	// nil di (feature off) is a no-op.
	di.Drain(ctx)

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

	// The pump exists to catch rows a full bus refused while the gateway was
	// live. Such a row is handed back to the pending set by
	// BaseChannel.publishInbound (spooler Release), and a live in-flight row is
	// born claimed, so the pump only ever sees rows nobody is working on and
	// cannot duplicate a message whose turn is still running.
	di.StartPump(ctx)

	// --- Graceful teardown plan -------------------------------------------
	//
	// The coordinator runs hooks LIFO (last registered, first to run), so the
	// registrations below are written in the REVERSE of the desired stop order.
	// Effective order on shutdown:
	//
	//	1. agent-drain     : let in-flight turns finish (10s budget)
	//	2. durable-inbound-shutdown : stop the pump, release our spool claims
	//	3. sessions-save   : persist every session to disk
	//	4. channels-stop   : stop accepting new inbound messages
	//	5. http-stop       : stop the unified server (API + Web UI)
	//	6. services-stop   : stop watchers/schedulers that may enqueue work
	//	7. lock-release    : last, so a restarting child never sees a live holder
	//
	// A hook that fails is recorded by the coordinator and does not abort the
	// rest: the process must never keep the instance lock because one stop step
	// returned an error.

	// Registered first => runs last. Critical: the lock release must not be
	// skipped by budget exhaustion, or a restarting child would have to steal a
	// still-held lock and fall back to the stale-timeout path.
	if desktop && instanceLock != nil {
		lock := instanceLock
		coord.RegisterCritical("lock-release", 2*time.Second, func(context.Context) error {
			return lock.Release()
		})
	}
	coord.Register("services-stop", 5*time.Second, func(context.Context) error {
		deviceService.Stop()
		heartbeatService.Stop()
		cronService.Stop()
		configWatcher.Stop()
		return nil
	})
	coord.Register("http-stop", 5*time.Second, func(hookCtx context.Context) error {
		return srv.Stop(hookCtx)
	})
	coord.Register("channels-stop", 5*time.Second, func(hookCtx context.Context) error {
		return channelManager.StopAll(hookCtx)
	})
	coord.Register("sessions-save", 5*time.Second, func(context.Context) error {
		if sm := agentLoop.SessionManager(); sm != nil {
			saved, failed := sm.SaveAll()
			logger.InfoCF("gateway", "Sessions flushed at shutdown", map[string]interface{}{
				"saved":  saved,
				"failed": failed,
			})
		}
		return nil
	})
	// Registered before agent-drain => runs second, right after in-flight
	// turns have finished their spool rows and before sessions are persisted.
	// Stopping the pump first is what makes the release meaningful: a pump
	// still ticking could claim rows again after they were handed back.
	coord.Register("durable-inbound-shutdown", 3*time.Second, func(context.Context) error {
		return di.Shutdown()
	})
	// Registered last => runs first.
	coord.Register("agent-drain", 10*time.Second, func(hookCtx context.Context) error {
		return agentLoop.Shutdown(hookCtx)
	})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(gatewayOut, "\nShutting down...")
	runGracefulShutdown(coord, agentLoop, cancel)
	fmt.Fprintln(gatewayOut, "✓ Gateway stopped")
}

// ──────────────────────────────────────────────────────────────────────────────
// Durable inbound wiring
// ──────────────────────────────────────────────────────────────────────────────

// durableInbound is the gateway's handle on the durable inbound spool: the
// service itself plus the pump's lifecycle. Every method is nil-safe, so the
// gateway can call Drain/StartPump/Shutdown unconditionally and a nil handle
// (feature off, no store) simply means "nothing happens".
//
// The pump is owned here rather than inside setupDurableInbound so the wiring
// step stays free of background goroutines: a unit test can build the handle
// and assert on it without a hidden ticker writing to the spool, and the
// gateway decides when replay starts and when the pump may run.
type durableInbound struct {
	inbound *durable.Inbound

	// mu guards the pump lifecycle. StartPump runs on the gateway goroutine
	// while Shutdown can arrive from the self-restart callback inside the
	// native channel, so the two must not read and clear cancel/done at the
	// same time.
	mu     sync.Mutex
	cancel context.CancelFunc // cancels the pump's context; nil until StartPump
	done   chan struct{}      // closed when StartPump returns; nil until StartPump
}

// setupDurableInbound wires the durable inbound spool into the agent loop and
// the channel manager. It returns nil when the feature is off, which is the
// signal for the gateway to keep its pre-durability behaviour: nothing is
// touched on agentLoop or channelManager in that case.
//
// The publish function handed to durable.NewInbound is the bus's own
// PublishInbound, and that is deliberate: NewInbound only ever publishes
// through it while REPLAYING a row it claimed, while live traffic is spooled
// and published by BaseChannel.publishInbound.
//
// The two paths are disjoint because of how the live row is claimed. Enqueue
// writes the row already claimed by this instance, so ClaimBatch - which only
// selects rows whose claimed_by is empty - never sees a message the agent loop
// is working on and cannot double-publish it. If the bus refuses that live
// publish, the channel calls Release, which hands exactly that one row back to
// the pending set for the pump to retry on its next tick. A replay pass only
// ever releases the ids it claimed itself, so it cannot touch a live row
// either. The blanket ReleaseClaims at shutdown stays: the pump is already
// stopped by then, so every claim still held is genuinely orphaned work for the
// successor to replay.
//
// Both setters must run before their consumer starts: SetInboundSpooler before
// channelManager.StartAll (channels read the field without a lock once they
// accept traffic) and SetInboundDurability before agentLoop.Run, for the same
// reason. This function only writes fields; it starts no goroutines and touches
// no rows.
func setupDurableInbound(
	enabled bool,
	spool *store.SpoolRepo,
	msgBus *bus.MessageBus,
	agentLoop *agent.AgentLoop,
	channelManager *channels.Manager,
) *durableInbound {
	// Flag off, no persistence, or no bus: leave both consumers untouched.
	if !enabled || spool == nil || msgBus == nil {
		return nil
	}

	d := durable.NewInbound(spool, msgBus.PublishInbound)
	if agentLoop != nil {
		agentLoop.SetInboundDurability(d)
	}
	if channelManager != nil {
		channelManager.SetInboundSpooler(d)
	}

	logger.InfoCF("durable", "Durable inbound enabled", map[string]interface{}{
		"instance": d.InstanceID(),
	})
	return &durableInbound{inbound: d}
}

// Drain replays persisted-but-unconsumed rows. The gateway calls it once, after
// the channels are wired and before channelManager.StartAll, so restored
// traffic reaches the loop ahead of anything new and no live message can be
// mistaken for a leftover mid-replay. The count it returns is logged inside
// durable.Inbound.Drain; a failed replay leaves the rows in the spool for the
// pump, so there is nothing for the caller to handle.
func (di *durableInbound) Drain(ctx context.Context) {
	if di == nil {
		return
	}
	if _, err := di.inbound.Drain(ctx); err != nil {
		logger.WarnCF("durable", "Durable inbound drain reported an error; the pump will retry",
			map[string]interface{}{"error": err.Error()})
	}
}

// StartPump launches the background pump that re-publishes rows a full bus
// refused, and returns immediately. It derives its own cancellable context from
// ctx so Shutdown can stop the pump without tearing down the gateway root
// context, and records the channel that lets Shutdown wait for the goroutine to
// actually leave. Calling it twice is a bug, not a supported state: the second
// pump would race the first over the same claims, so it is refused.
func (di *durableInbound) StartPump(ctx context.Context) {
	if di == nil {
		return
	}

	di.mu.Lock()
	if di.done != nil {
		di.mu.Unlock()
		logger.WarnC("durable", "Durable inbound pump already started; ignoring duplicate start")
		return
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	di.cancel = cancel
	di.done = done
	di.mu.Unlock()

	go func() {
		defer close(done)
		di.inbound.StartPump(pumpCtx)
	}()
}

// Shutdown stops the pump, waits for its goroutine to exit, and returns this
// instance's claims to the pending set so the next process re-drains them
// immediately instead of waiting out durable.StaleClaimTimeout. It is safe to
// call when the pump never started or when called twice.
//
// The order matters: releasing before the pump is gone would let a tick in
// flight re-claim the rows this shutdown is handing back.
func (di *durableInbound) Shutdown() error {
	if di == nil {
		return nil
	}

	// Hand the pump's handles over to this caller alone, then stop waiting on
	// them without holding the lock: a concurrent StartPump must not be able
	// to claim rows behind our back, and a second Shutdown finds nothing to
	// wait for.
	di.mu.Lock()
	cancel, done := di.cancel, di.done
	di.cancel, di.done = nil, nil
	di.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	released, err := di.inbound.ReleaseClaims()
	if err != nil {
		logger.WarnCF("durable", "Releasing inbound claims at shutdown failed",
			map[string]interface{}{"count": released, "error": err.Error()})
		return err
	}
	logger.InfoCF("durable", "Released inbound claims at shutdown",
		map[string]interface{}{"count": released})
	return nil
}

// teardownOnce maps a coordinator to the sync.Once that guards its teardown.
//
// The once is keyed by coordinator rather than being a single package-level
// value because idempotency is a property of a specific teardown plan: the
// signal path and the restart path share one coordinator (so the second trigger
// must be a no-op) while an unrelated coordinator must still run its own hooks.
// The gateway creates exactly one coordinator for its whole lifetime, so the
// single entry this map holds lives as long as the process.
var teardownOnce sync.Map // *update.ShutdownCoordinator -> *sync.Once

// teardownGuard returns the sync.Once owned by coord, creating it on first use.
// It fast-paths a Load so the steady state (the guard already exists) does not
// allocate a throwaway Once on every call.
func teardownGuard(coord *update.ShutdownCoordinator) *sync.Once {
	if actual, ok := teardownOnce.Load(coord); ok {
		return actual.(*sync.Once)
	}
	actual, _ := teardownOnce.LoadOrStore(coord, &sync.Once{})
	return actual.(*sync.Once)
}

// runGracefulShutdown performs the gateway's ordered teardown exactly once:
// first every registered shutdown hook (LIFO, budget-bounded, failures recorded
// and never fatal), then AgentLoop.Stop, and only then cancel, which releases
// the root context. Cancelling before the hooks would abort the in-flight turns
// the agent-drain hook exists to let finish.
//
// Idempotent per coordinator: a repeat call (the SIGTERM path firing right
// after a self-restart did the teardown) skips RunAll and Stop but still calls
// cancel, since context.CancelFunc is safe to call more than once.
func runGracefulShutdown(coord *update.ShutdownCoordinator, al *agent.AgentLoop, cancel func()) {
	once := teardownGuard(coord)
	once.Do(func() {
		results := coord.RunAll(context.Background())
		// A failing hook is reported here and never aborts the teardown.
		for name, err := range results {
			if err != nil {
				logger.WarnCF("gateway", "Shutdown hook reported an error", map[string]interface{}{
					"hook":  name,
					"error": err.Error(),
				})
			}
		}
		if al != nil {
			al.Stop()
		}
	})
	if cancel != nil {
		cancel()
	}
}

// instanceLockHandoffTimeout bounds how long a self-restart child waits for the
// previous instance to release the desktop lock. It must comfortably exceed the
// shutdown budget (update.DefaultShutdownBudget) plus the agent drain.
var instanceLockHandoffTimeout = 20 * time.Second

// instanceLockHandoffPoll is the retry interval while waiting for the handoff.
var instanceLockHandoffPoll = 100 * time.Millisecond

// acquireInstanceLock takes the desktop instance lock at path.
//
// Behaviour depends on why this process is starting:
//
//   - Normal start (LELE_RESTART_CHILD unset): a single attempt. A live holder
//     is reported immediately as *lockfile.AlreadyRunningError so the desktop
//     shell can show "already running" instead of hanging.
//   - Self-restart child (LELE_RESTART_CHILD=1): the parent deliberately keeps
//     the lock while it drains in-flight turns, so the child retries until the
//     parent's lock-release hook runs or instanceLockHandoffTimeout expires.
//
// Staleness is never decided here: lockfile.Acquire already steals a lock whose
// holder is dead, so this function only retries while it reports a live holder.
func acquireInstanceLock(path string) (*lockfile.Lock, error) {
	if os.Getenv(update.RestartChildEnvKey) != "1" {
		return lockfile.Acquire(path)
	}

	deadline := time.Now().Add(instanceLockHandoffTimeout)
	var lastErr error
	for {
		lock, err := lockfile.Acquire(path)
		if err == nil {
			return lock, nil
		}
		var arErr *lockfile.AlreadyRunningError
		if !errors.As(err, &arErr) {
			return nil, err
		}
		lastErr = err
		if time.Now().Add(instanceLockHandoffPoll).After(deadline) {
			break
		}
		logger.InfoCF("gateway", "Waiting for previous instance to release the lock", map[string]interface{}{
			"lock": path,
			"pid":  arErr.PID,
		})
		time.Sleep(instanceLockHandoffPoll)
	}

	return nil, fmt.Errorf("previous gateway instance (lock %s) did not release it within %s: %w",
		path, instanceLockHandoffTimeout, lastErr)
}

func setupCronTool(executor tools.JobExecutor, al *agent.AgentLoop, msgBus *bus.MessageBus, workspace string, restrict bool, execTimeout time.Duration, config *config.Config) *cron.CronService {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath, nil)

	cronTool := tools.NewCronTool(cronService, executor, msgBus, workspace, restrict, execTimeout, config)
	// Let ExecuteJob detect when a job's `to` field designates an existing
	// session so notifications land in that chat instead of a synthetic
	// cron-<jobID> session.
	cronTool.SetSessionExistsCallback(func(sessionKey string) bool {
		sm := al.SessionManager()
		if sm == nil {
			return false
		}
		return sm.SessionExists(sessionKey)
	})
	al.RegisterTool(cronTool)

	cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		result := cronTool.ExecuteJob(context.Background(), job)
		if strings.HasPrefix(result, "Error:") {
			return result, fmt.Errorf("%s", result)
		}
		return result, nil
	})

	return cronService
}
