package tui

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"

	tea "github.com/charmbracelet/bubbletea"
)

type streamThrottleMsg struct{}

func (m *Model) throttledUpdateViewport() tea.Cmd {
	if m.streamThrottleActive {
		m.streamPendingUpdate = true
		return nil
	}

	m.updateViewport()
	m.streamThrottleActive = true
	m.streamPendingUpdate = false

	return tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
		return streamThrottleMsg{}
	})
}

func (m *Model) flushStreamUpdate() {
	if m.streamPendingUpdate {
		m.streamPendingUpdate = false
		m.updateViewport()
	}
	m.streamThrottleActive = false
}

func (m *Model) completeCmd() tea.Cmd {
	key := m.currentKey
	return func() tea.Msg {
		return completeMsg{sessionKey: key}
	}
}

func (m *Model) startOutboundListener() tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case <-m.ctx.Done():
				return nil
			default:
				outMsg, ok := m.agentLoop.MessageBus().SubscribeOutbound(m.ctx)
				if !ok {
					return nil
				}
				return outboundMsg{msg: outMsg}
			}
		}
	}
}

func (m *Model) tickCmd() tea.Cmd {
	if m.tickPending {
		return nil
	}
	m.tickPending = true
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) submitMessage() tea.Cmd {
	content := strings.TrimSpace(m.chatInput.Value())
	if content == "" {
		return nil
	}
	m.chatInput.SetValue("")
	return m.publishUserMessage(content)
}

func (m *Model) publishUserMessage(content string) tea.Cmd {
	if m.currentKey == "" {
		m.createNewChat()
	}
	m.showWelcome = false

	m.compactFeedback = ""
	m.statusFeedback = ""
	m.queueFeedback = ""
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.subagentContinuationPending = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""

	m.pendingUserMessage = strings.TrimSpace(content)
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    content,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return m.tickCmd()
}

const customCommandsRefreshTTL = 30 * time.Second

type harnessCommandSource interface {
	HarnessCommands() []*harness.Command
}

func (m *Model) customCommands() []commandInfo {
	if m.agentLoop == nil {
		return nil
	}
	if m.customCmds != nil && time.Since(m.customCmdsAt) < customCommandsRefreshTTL {
		return m.customCmds
	}

	src, ok := interface{}(m.agentLoop).(harnessCommandSource)
	if !ok {
		m.customCmds = []commandInfo{}
		m.customCmdsAt = time.Now()
		return nil
	}

	cmds := src.HarnessCommands()
	list := make([]commandInfo, 0, len(cmds))
	for _, c := range cmds {
		if c == nil || c.Name == "" {
			continue
		}
		list = append(list, commandInfo{name: "/" + c.Name, description: harnessCommandDescription(c)})
	}

	m.customCmds = list
	m.customCmdsAt = time.Now()
	return list
}

func harnessCommandDescription(c *harness.Command) string {
	desc := c.Description
	var suffix []string
	if c.Agent != "" {
		suffix = append(suffix, "agent: "+c.Agent)
	}
	if c.Model != "" {
		suffix = append(suffix, "model: "+c.Model)
	}
	if len(suffix) > 0 {
		if desc != "" {
			desc += " · "
		}
		desc += strings.Join(suffix, " · ")
	}
	return desc
}

const commandAppliedArgsRunes = 60

func formatCommandApplied(meta map[string]string) string {
	name := meta["command"]
	if name == "" {
		return ""
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	line := "⚡ " + name + " applied"

	args := strings.TrimSpace(meta["args"])
	if args != "" {
		if len([]rune(args)) > commandAppliedArgsRunes {
			args = truncateRunes(args, commandAppliedArgsRunes) + "…"
		}
		line += " " + args
	}
	if agent := meta["agent"]; agent != "" {
		line += " · agent: " + agent
	}
	if model := meta["model"]; model != "" {
		line += " · model: " + model
	}
	return sanitizeDisplayText(line)
}

func (m *Model) isCustomCommand(name string) bool {
	want := strings.ToLower(strings.TrimPrefix(name, "/"))
	if want == "" {
		return false
	}
	for _, c := range m.customCommands() {
		if strings.ToLower(strings.TrimPrefix(c.name, "/")) == want {
			return true
		}
	}
	return false
}

func (m *Model) filterAutocomplete(val string) {
	m.autocompleteItems = nil
	for _, cmd := range allCommands {
		if strings.HasPrefix(cmd.name, val) {
			m.autocompleteItems = append(m.autocompleteItems, cmd)
		}
	}
	for _, cmd := range m.customCommands() {
		if !strings.HasPrefix(cmd.name, val) {
			continue
		}
		builtin := false
		for _, b := range allCommands {
			if b.name == cmd.name {
				builtin = true
				break
			}
		}
		if builtin {
			continue
		}
		m.autocompleteItems = append(m.autocompleteItems, cmd)
	}
	sortCommandsByUsage(m.autocompleteItems, m.cmdUsage)
	if m.autocompleteIdx < 0 || m.autocompleteIdx >= len(m.autocompleteItems) {
		m.autocompleteIdx = 0
	}
}

func (m *Model) getGroupProfiles() []config.GroupProfile {
	snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.Groups.List
}

func (m *Model) submitGroupStart(profileID, task string) tea.Cmd {
	groupCmd := groupStartCommand(profileID, task)

	if m.currentKey == "" {
		m.createNewChat()
	}
	m.showWelcome = false
	m.chatInput.SetValue("")
	m.compactFeedback = ""
	m.statusFeedback = ""
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.subagentContinuationPending = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    groupCmd,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return m.tickCmd()
}

func (m *Model) recordGroupCompleteStatus(groupID string) {
	if m.groupStatus == nil {
		m.groupStatus = make(map[string]string)
	}
	switch m.groupStatus[groupID] {
	case "done", "stopped", "error":
	case "started", "":
		m.groupStatus[groupID] = "done"
	default:
		m.groupStatus[groupID] = "done"
	}
}
