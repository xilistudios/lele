package heartbeat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/tools"
)

const (
	minIntervalMinutes     = 5
	defaultIntervalMinutes = 30
)

type HeartbeatHandler func(prompt, channel, chatID string) *tools.ToolResult

type HeartbeatService struct {
	workspace string
	bus       *bus.MessageBus
	state     *state.Manager
	handler   HeartbeatHandler
	interval  time.Duration
	enabled   bool
	mu        sync.RWMutex
	stopChan  chan struct{}
	done      chan struct{}
	stopping  []chan struct{}
}

func NewHeartbeatService(workspace string, intervalMinutes int, enabled bool) *HeartbeatService {
	if intervalMinutes < minIntervalMinutes && intervalMinutes != 0 {
		intervalMinutes = minIntervalMinutes
	}

	if intervalMinutes == 0 {
		intervalMinutes = defaultIntervalMinutes
	}

	return &HeartbeatService{
		workspace: workspace,
		interval:  time.Duration(intervalMinutes) * time.Minute,
		enabled:   enabled,
		state:     state.NewManager(workspace),
	}
}

func (hs *HeartbeatService) SetBus(msgBus *bus.MessageBus) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.bus = msgBus
}

func (hs *HeartbeatService) SetHandler(handler HeartbeatHandler) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.handler = handler
}

func (hs *HeartbeatService) startLocked() {
	if hs.stopChan != nil {
		return
	}
	if !hs.enabled {
		return
	}
	hs.stopChan = make(chan struct{})
	hs.done = make(chan struct{})
	stopChan := hs.stopChan
	done := hs.done
	go func() {
		defer close(done)
		hs.runLoop(stopChan)
	}()
}

func (hs *HeartbeatService) Start() error {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	hs.startLocked()
	return nil
}

func (hs *HeartbeatService) stopLocked() {
	if hs.stopChan == nil {
		return
	}

	close(hs.stopChan)
	if hs.done != nil {
		hs.stopping = append(hs.stopping, hs.done)
	}
	hs.stopChan = nil
	hs.done = nil
}

func (hs *HeartbeatService) takeDoneLocked() []chan struct{} {
	hs.stopLocked()
	if len(hs.stopping) == 0 {
		return nil
	}
	done := hs.stopping
	hs.stopping = nil
	return done
}

func (hs *HeartbeatService) Stop() {
	hs.mu.Lock()
	done := hs.takeDoneLocked()
	hs.mu.Unlock()

	for _, ch := range done {
		<-ch
	}
}

func (hs *HeartbeatService) UpdateConfig(intervalMinutes int, enabled bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if intervalMinutes < minIntervalMinutes && intervalMinutes != 0 {
		intervalMinutes = minIntervalMinutes
	}
	if intervalMinutes == 0 {
		intervalMinutes = defaultIntervalMinutes
	}
	hs.interval = time.Duration(intervalMinutes) * time.Minute
	hs.enabled = enabled
	if enabled && hs.stopChan == nil {
		hs.startLocked()
	}
	if !enabled {
		hs.stopLocked()
	}
}

func (hs *HeartbeatService) IsRunning() bool {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return hs.stopChan != nil
}

func (hs *HeartbeatService) runLoop(stopChan chan struct{}) {
	hs.mu.RLock()
	curInterval := hs.interval
	hs.mu.RUnlock()
	ticker := time.NewTicker(curInterval)
	defer ticker.Stop()

	select {
	case <-stopChan:
		return
	case <-time.After(time.Second):
		hs.executeHeartbeat()
	}

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			hs.executeHeartbeat()
			hs.mu.RLock()
			newInterval := hs.interval
			hs.mu.RUnlock()
			if newInterval != curInterval {
				curInterval = newInterval
				ticker.Reset(curInterval)
			}
		}
	}
}

func (hs *HeartbeatService) executeHeartbeat() {
	hs.mu.RLock()
	enabled := hs.enabled
	handler := hs.handler
	if !hs.enabled || hs.stopChan == nil {
		hs.mu.RUnlock()
		return
	}
	hs.mu.RUnlock()

	if !enabled {
		return
	}

	logger.DebugC("heartbeat", "Executing heartbeat")

	prompt := hs.buildPrompt()
	if prompt == "" {
		logger.InfoC("heartbeat", "No heartbeat prompt (HEARTBEAT.md empty or missing)")
		return
	}

	if handler == nil {
		hs.logError("Heartbeat handler not configured")
		return
	}

	lastChannel := hs.state.GetLastChannel()
	channel, chatID := hs.parseLastChannel(lastChannel)

	hs.logInfo("Resolved channel: %s, chatID: %s (from lastChannel: %s)", channel, chatID, lastChannel)

	result := handler(prompt, channel, chatID)

	if result == nil {
		hs.logInfo("Heartbeat handler returned nil result")
		return
	}

	if result.IsError {
		hs.logError("Heartbeat error: %s", result.ForLLM)
		return
	}

	if result.Async {
		hs.logInfo("Async task started: %s", result.ForLLM)
		logger.InfoCF("heartbeat", "Async heartbeat task started",
			map[string]interface{}{
				"message": result.ForLLM,
			})
		return
	}

	if result.Silent {
		hs.logInfo("Heartbeat OK - silent")
		return
	}

	if result.ForUser != "" {
		hs.sendResponse(result.ForUser)
	} else if result.ForLLM != "" {
		hs.sendResponse(result.ForLLM)
	}

	hs.logInfo("Heartbeat completed: %s", result.ForLLM)
}

func (hs *HeartbeatService) buildPrompt() string {
	heartbeatPath := filepath.Join(hs.workspace, "HEARTBEAT.md")

	data, err := os.ReadFile(heartbeatPath)
	if err != nil {
		if os.IsNotExist(err) {
			hs.createDefaultHeartbeatTemplate()
			return ""
		}
		hs.logError("Error reading HEARTBEAT.md: %v", err)
		return ""
	}

	content := string(data)
	if len(content) == 0 {
		return ""
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	return fmt.Sprintf(`# Heartbeat Check

Current time: %s

You are a proactive AI assistant. This is a scheduled heartbeat check.
Review the following tasks and execute any necessary actions using available skills.
If there is nothing that requires attention, respond ONLY with: HEARTBEAT_OK

## IMPORTANT RESTRICTIONS
- DO NOT use "docker" commands for system checks (use "systemctl is-active docker" instead)
- DO NOT use "docker ps" or any docker container listing commands
- These commands require approval and will block the heartbeat

## System Check Alternatives
- Check Docker service: "systemctl is-active docker"
- Check services: "systemctl status <service-name>"
- Check system: "uptime", "free -h", "df -h"

%s
`, now, content)
}

func (hs *HeartbeatService) createDefaultHeartbeatTemplate() {
	heartbeatPath := filepath.Join(hs.workspace, "HEARTBEAT.md")

	defaultContent := `# Heartbeat Check List

This file contains tasks for the heartbeat service to check periodically.

## Examples

- Check for unread messages
- Review upcoming calendar events
- Check device status (e.g., MaixCam)

## Instructions

- Execute ALL tasks listed below. Do NOT skip any task.
- For simple tasks (e.g., report current time), respond directly.
- For complex tasks that may take time, use the spawn tool to create a subagent.
- The spawn tool is async - subagent results will be sent to the user automatically.
- After spawning a subagent, CONTINUE to process remaining tasks.
- Only respond with HEARTBEAT_OK when ALL tasks are done AND nothing needs attention.

---

Add your heartbeat tasks below this line:
`

	if err := os.WriteFile(heartbeatPath, []byte(defaultContent), 0644); err != nil {
		hs.logError("Failed to create default HEARTBEAT.md: %v", err)
	} else {
		hs.logInfo("Created default HEARTBEAT.md template")
	}
}

func (hs *HeartbeatService) sendResponse(response string) {
	hs.mu.RLock()
	msgBus := hs.bus
	hs.mu.RUnlock()

	if msgBus == nil {
		hs.logInfo("No message bus configured, heartbeat result not sent")
		return
	}

	lastChannel := hs.state.GetLastChannel()
	if lastChannel == "" {
		hs.logInfo("No last channel recorded, heartbeat result not sent")
		return
	}

	platform, userID := hs.parseLastChannel(lastChannel)

	if platform == "" || userID == "" {
		return
	}

	msgBus.PublishOutbound(bus.OutboundMessage{
		Channel: platform,
		ChatID:  userID,
		Content: response,
	})

	hs.logInfo("Heartbeat result sent to %s", platform)
}

func (hs *HeartbeatService) parseLastChannel(lastChannel string) (platform, userID string) {
	if lastChannel == "" {
		return "", ""
	}

	parts := strings.SplitN(lastChannel, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		hs.logError("Invalid last channel format: %s", lastChannel)
		return "", ""
	}

	platform, userID = parts[0], parts[1]

	if constants.IsInternalChannel(platform) {
		hs.logInfo("Skipping internal channel: %s", platform)
		return "", ""
	}

	return platform, userID
}

func (hs *HeartbeatService) logInfo(format string, args ...any) {
	hs.log("INFO", format, args...)
}

func (hs *HeartbeatService) logError(format string, args ...any) {
	hs.log("ERROR", format, args...)
}

func (hs *HeartbeatService) log(level, format string, args ...any) {
	logFile := filepath.Join(hs.workspace, "heartbeat.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "[%s] [%s] %s\n", timestamp, level, fmt.Sprintf(format, args...))
}
