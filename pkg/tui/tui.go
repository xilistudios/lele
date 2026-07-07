package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paneType int

const (
	ChatsPane paneType = iota
	ChatViewPane
)

type modalType int

const (
	ModalNone modalType = iota
	ModalSessions
	ModalAgent
	ModalModel
)

type commandInfo struct {
	name        string
	description string
}

var allCommands = []commandInfo{
	{name: "/sessions", description: "Switch session"},
	{name: "/new", description: "New session"},
	{name: "/agents", description: "Switch agent"},
	{name: "/models", description: "Switch model"},
	{name: "/clear", description: "Clear session history"},
	{name: "/quit", description: "Exit TUI"},
}

// Messages for Bubbletea loop
type sessionListMsg []*session.Session
type outboundMsg struct {
	msg bus.OutboundMessage
}
type completeMsg struct {
	sessionKey string
}
type tickMsg time.Time

type Model struct {
	agentLoop          *agent.AgentLoop
	sessionMgr         *session.SessionManager
	cfg                *config.Config
	ctx                context.Context
	cancel             context.CancelFunc

	// UI state
	activePane         paneType
	selectedSessionIdx int
	visibleSessions    []*session.Session
	currentKey         string

	// Autocomplete dropdown menu state
	showAutocomplete  bool
	autocompleteItems []commandInfo
	autocompleteIdx   int

	// Selection modals
	modalMode          modalType
	modalItems         []string
	modalSelectedIdx   int

	// Sub-components
	viewport           viewport.Model
	textInput          textinput.Model

	// Message processing / streaming
	processing         bool
	currentMessageID   string
	currentStream      string
	currentThinking    string
	startTime          time.Time
	elapsedTime        time.Duration
	lastDuration       time.Duration
	animationTick      int

	// Workspace Git info
	gitBranch          string
	workspacePath      string

	// Terminal size
	width              int
	height             int
}

func NewModel(cfg *config.Config, agentLoop *agent.AgentLoop, sessionMgr *session.SessionManager) *Model {
	ti := textinput.New()
	ti.Placeholder = "Ask anything... \"What is the tech stack of this project?\""
	ti.Focus()
	ti.CharLimit = 4096
	ti.Width = 80
	ti.Prompt = " "

	vp := viewport.New(80, 20)
	vp.SetContent("Selecciona o crea un chat para comenzar.")

	ctx, cancel := context.WithCancel(context.Background())
	workspacePath := cfg.WorkspacePath()
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}

	m := &Model{
		agentLoop:     agentLoop,
		sessionMgr:    sessionMgr,
		cfg:           cfg,
		ctx:           ctx,
		cancel:        cancel,
		viewport:      vp,
		textInput:     ti,
		activePane:    ChatViewPane,
		workspacePath: workspacePath,
		gitBranch:     getGitBranch(workspacePath),
	}

	return m
}

func getGitBranch(dir string) string {
	headPath := filepath.Join(dir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		if strings.HasPrefix(content, "ref: refs/heads/") {
			return strings.TrimPrefix(content, "ref: refs/heads/")
		}
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return "main"
}

func wrapText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string

	for _, line := range lines {
		if len(line) <= limit {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		currentLine := words[0]
		for _, word := range words[1:] {
			if len(currentLine)+1+len(word) <= limit {
				currentLine += " " + word
			} else {
				wrappedLines = append(wrappedLines, currentLine)
				currentLine = word
			}
		}
		wrappedLines = append(wrappedLines, currentLine)
	}

	return strings.Join(wrappedLines, "\n")
}

func (m *Model) Init() tea.Cmd {
	m.reloadSessions()
	return tea.Batch(
		textinput.Blink,
		m.startOutboundListener(),
	)
}

func (m *Model) reloadSessions() {
	m.visibleSessions = nil
	all := m.sessionMgr.ListSessions()

	if len(all) == 0 && m.currentKey == "" {
		m.createNewChat()
		all = m.sessionMgr.ListSessions()
	}

	for _, s := range all {
		if len(s.Messages) > 0 || s.Key == m.currentKey {
			m.visibleSessions = append(m.visibleSessions, s)
		}
	}

	if m.currentKey != "" {
		found := false
		for i, s := range m.visibleSessions {
			if s.Key == m.currentKey {
				m.selectedSessionIdx = i
				found = true
				break
			}
		}
		if !found && len(m.visibleSessions) > 0 {
			m.selectedSessionIdx = 0
			m.currentKey = m.visibleSessions[0].Key
		}
	} else if len(m.visibleSessions) > 0 {
		m.selectedSessionIdx = 0
		m.currentKey = m.visibleSessions[0].Key
	}

	m.updateViewport()
}

func (m *Model) createNewChat() {
	newKey := fmt.Sprintf("tui:chat:%s", uuid.New().String())
	m.sessionMgr.GetOrCreate(newKey)

	defaultAgentID := m.agentLoop.GetProvidable().GetDefaultAgentID()
	m.agentLoop.GetProvidable().SetSessionAgent(newKey, defaultAgentID)

	m.currentKey = newKey
}

func (m *Model) updateViewport() {
	if m.currentKey == "" {
		m.viewport.SetContent("Crea un chat para comenzar.")
		return
	}

	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)

	var sb strings.Builder
	for _, msg := range history {
		if msg.Role == "user" {
			sb.WriteString(UserRoleStyle.Render("Tú") + "\n")
			sb.WriteString(UserMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
		} else if msg.Role == "assistant" {
			agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
			agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
			agentName := agentID
			if ok && agentInfo.Name != "" {
				agentName = agentInfo.Name
			}

			sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")

			if msg.ReasoningContent != "" {
				sb.WriteString(ThinkingContentStyle.Render(wrapText(msg.ReasoningContent, m.viewport.Width-6)) + "\n")
			}

			sb.WriteString(AssistantMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
		} else if msg.Role == "system" {
			sb.WriteString(SystemRoleStyle.Render("System") + "\n")
			sb.WriteString(SystemMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
		}
	}

	if m.processing && (m.currentStream != "" || m.currentThinking != "") {
		agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
		agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
		agentName := agentID
		if ok && agentInfo.Name != "" {
			agentName = agentInfo.Name
		}

		sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")

		if m.currentThinking != "" {
			sb.WriteString(ThinkingContentStyle.Render(wrapText(m.currentThinking, m.viewport.Width-6)) + "\n")
		}
		if m.currentStream != "" {
			sb.WriteString(AssistantMessageStyle.Render(wrapText(m.currentStream, m.viewport.Width-4)) + "\n")
		}
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
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

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) submitMessage() tea.Cmd {
	content := strings.TrimSpace(m.textInput.Value())
	if content == "" {
		return nil
	}

	m.textInput.SetValue("")
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.currentStream = ""
	m.currentThinking = ""

	m.sessionMgr.AddMessage(m.currentKey, "user", content)
	m.sessionMgr.Save(m.currentKey)
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    content,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return tickCmd()
}

func (m *Model) filterAutocomplete(val string) {
	m.autocompleteItems = nil
	for _, cmd := range allCommands {
		if strings.HasPrefix(cmd.name, val) {
			m.autocompleteItems = append(m.autocompleteItems, cmd)
		}
	}
	if m.autocompleteIdx >= len(m.autocompleteItems) {
		m.autocompleteIdx = 0
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.modalMode != ModalNone {
			switch msg.String() {
			case "up", "k":
				if m.modalSelectedIdx > 0 {
					m.modalSelectedIdx--
				}
			case "down", "j":
				if m.modalSelectedIdx < len(m.modalItems)-1 {
					m.modalSelectedIdx++
				}
			case "enter":
				selectedVal := m.modalItems[m.modalSelectedIdx]
				if m.modalMode == ModalAgent {
					m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, selectedVal)
				} else if m.modalMode == ModalModel {
					m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
				} else if m.modalMode == ModalSessions {
					parts := strings.SplitN(selectedVal, " | ", 2)
					if len(parts) > 0 {
						for _, s := range m.visibleSessions {
							name := s.Name
							if name == "" {
								name = "Nuevo Chat"
							}
							if s.Key == parts[0] || name == parts[0] {
								m.currentKey = s.Key
								break
							}
						}
					}
				}
				m.modalMode = ModalNone
				m.reloadSessions()
			case "esc", "q":
				m.modalMode = ModalNone
			}
			return m, nil
		}

		if m.showAutocomplete {
			switch msg.String() {
			case "up", "ctrl+k":
				if m.autocompleteIdx > 0 {
					m.autocompleteIdx--
				} else {
					m.autocompleteIdx = len(m.autocompleteItems) - 1
				}
				return m, nil
			case "down", "ctrl+j":
				if m.autocompleteIdx < len(m.autocompleteItems)-1 {
					m.autocompleteIdx++
				} else {
					m.autocompleteIdx = 0
				}
				return m, nil
			case "tab", "enter":
				if len(m.autocompleteItems) > 0 {
					completed := m.autocompleteItems[m.autocompleteIdx].name
					m.textInput.SetValue(completed)
					m.showAutocomplete = false
					if msg.String() == "enter" {
						m.executeCommand(completed)
						m.textInput.SetValue("")
					}
				}
				return m, nil
			case "esc":
				m.showAutocomplete = false
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "ctrl+p":
			m.showAutocomplete = true
			m.filterAutocomplete("/")
			return m, nil

		case "up", "down":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)

		case "enter":
			inputVal := m.textInput.Value()
			if strings.HasPrefix(inputVal, "/") {
				m.executeCommand(inputVal)
				m.textInput.SetValue("")
			} else if !m.processing {
				cmd := m.submitMessage()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case tickMsg:
		if m.processing {
			m.elapsedTime = time.Since(m.startTime)
			m.animationTick++
			cmds = append(cmds, tickCmd())
		}

	case outboundMsg:
		if msg.msg.ChatID == m.currentKey {
			switch msg.msg.Event {
			case "message.stream":
				m.currentStream += msg.msg.Content
				m.updateViewport()

				if msg.msg.Metadata != nil && msg.msg.Metadata["done"] == "true" {
					cmds = append(cmds, func() tea.Msg {
						return completeMsg{sessionKey: m.currentKey}
					})
				}
			case "message.thinking":
				m.currentThinking += msg.msg.Content
				m.updateViewport()
			case "":
				if m.processing && (msg.msg.MessageID == m.currentMessageID || msg.msg.ReplyTo == m.currentMessageID) {
					cmds = append(cmds, func() tea.Msg {
						return completeMsg{sessionKey: m.currentKey}
					})
				}
			}
		}
		cmds = append(cmds, m.startOutboundListener())

	case completeMsg:
		if msg.sessionKey == m.currentKey {
			m.processing = false
			m.lastDuration = time.Since(m.startTime)
			m.currentStream = ""
			m.currentThinking = ""
			m.currentMessageID = ""
			m.reloadSessions()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = int(float64(m.width)*0.78) - 4
		m.viewport.Height = m.height - 8
		m.textInput.Width = int(float64(m.width)*0.78) - 4
		m.updateViewport()
	}

	if m.modalMode == ModalNone {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)

		val := m.textInput.Value()
		if strings.HasPrefix(val, "/") {
			m.showAutocomplete = true
			m.filterAutocomplete(val)
		} else {
			m.showAutocomplete = false
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) executeCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/sessions":
		m.modalMode = ModalSessions
		m.modalItems = nil
		for _, s := range m.visibleSessions {
			name := s.Name
			if name == "" {
				name = "Nuevo Chat"
			}
			m.modalItems = append(m.modalItems, fmt.Sprintf("%s | %d msgs", name, len(s.Messages)))
		}
		m.modalSelectedIdx = 0

	case "/new":
		m.createNewChat()
		m.reloadSessions()

	case "/agents":
		m.modalMode = ModalAgent
		m.modalItems = m.agentLoop.GetProvidable().ListAvailableAgentIDs()
		m.modalSelectedIdx = 0

	case "/models":
		m.modalMode = ModalModel
		m.modalItems = nil
		cfgSnapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
		if cfgSnapshot != nil && cfgSnapshot.Providers != nil {
			providersMap := cfgSnapshot.Providers.ListNamed()
			var pNames []string
			for k := range providersMap {
				pNames = append(pNames, k)
			}
			sort.Strings(pNames)
			for _, pName := range pNames {
				pCfg := providersMap[pName]
				var aliases []string
				for mAlias := range pCfg.Models {
					aliases = append(aliases, mAlias)
				}
				sort.Strings(aliases)
				for _, mAlias := range aliases {
					m.modalItems = append(m.modalItems, fmt.Sprintf("%s:%s", pName, mAlias))
				}
			}
		}
		m.modalSelectedIdx = 0

	case "/clear":
		m.agentLoop.GetProvidable().ClearSession(m.currentKey)
		m.reloadSessions()

	case "/quit":
		m.cancel()
		os.Exit(0)
	}
}

func (m *Model) currentSessionKey() string {
	return m.currentKey
}

func (m *Model) getHistoryMessageCount() int {
	if m.currentKey == "" {
		return 0
	}
	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	return count
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Inicializando..."
	}

	historyCount := m.getHistoryMessageCount()

	// --------------------------------------------------------------------------
	// WELCOME HOME SCREEN LAYOUT (If session is empty / new)
	// --------------------------------------------------------------------------
	if historyCount == 0 && !m.processing {
		var welcomeBuilder strings.Builder

		logo := "\n\n" +
			"  _      ______ _      ______\n" +
			" | |    |  ____| |    |  ____|\n" +
			" | |    | |__  | |    | |__   \n" +
			" | |    |  __| | |    |  __|\n" +
			" | |____| |____| |____| |____\n" +
			" |______|______|______|______|\n\n"
		welcomeBuilder.WriteString(WelcomeLogo.Render(logo) + "\n\n")

		var autocompleteView string
		if m.showAutocomplete && len(m.autocompleteItems) > 0 {
			var autoSb strings.Builder
			for i, cmd := range m.autocompleteItems {
				line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
				if i == m.autocompleteIdx {
					autoSb.WriteString(ModalItemActive.Render(line) + "\n")
				} else {
					autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
				}
			}
			autocompleteView = ModalContainer.Width(60).Render(autoSb.String()) + "\n"
		}

		inputView := InputBarContainer.Width(60).Render(m.textInput.View())
		
		if autocompleteView != "" {
			welcomeBuilder.WriteString(lipgloss.Place(m.width, len(m.autocompleteItems)+2, lipgloss.Center, lipgloss.Center, autocompleteView) + "\n")
		}
		welcomeBuilder.WriteString(lipgloss.Place(m.width, 3, lipgloss.Center, lipgloss.Center, inputView) + "\n")

		agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
		modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
		thinkLevel := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
		if thinkLevel == "" {
			thinkLevel = "off"
		}
		infoText := fmt.Sprintf("Agente: %s  ·  Modelo: %s  ·  Thinking: %s", agentID, modelName, thinkLevel)
		welcomeBuilder.WriteString(lipgloss.Place(m.width, 1, lipgloss.Center, lipgloss.Center, HelpStyle.Render(infoText)) + "\n\n")

		shortcuts := "tab switch pane  ·  ctrl+p commands"
		welcomeBuilder.WriteString(lipgloss.Place(m.width, 1, lipgloss.Center, lipgloss.Center, HelpStyle.Render(shortcuts)) + "\n\n\n")

		tip := "● Tip Press ctrl+a for agents, ctrl+m for models, type / for commands"
		welcomeBuilder.WriteString(lipgloss.Place(m.width, 1, lipgloss.Center, lipgloss.Center, WelcomeTip.Render(tip)) + "\n")

		return AppContainer.Width(m.width).Height(m.height).Render(welcomeBuilder.String())
	}

	// --------------------------------------------------------------------------
	// SPLIT COLUMN CONVERSATIONAL LAYOUT
	// --------------------------------------------------------------------------
	leftWidth := int(float64(m.width) * 0.78)
	rightWidth := m.width - leftWidth - 3
	contentHeight := m.height - 2

	// Render Left Column (Chat Contents)
	var leftBuilder strings.Builder

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
	if thinkLevel == "" {
		thinkLevel = "off"
	}
	headerText := fmt.Sprintf(" %s  ·  %s  ·  %s", agentID, modelName, thinkLevel)
	leftBuilder.WriteString(HeaderStyle.Render(headerText) + "\n")

	m.viewport.Width = leftWidth - 2
	m.viewport.Height = contentHeight - 6
	leftBuilder.WriteString(ViewportStyle.Render(m.viewport.View()) + "\n")

	var statusLine string
	if m.processing {
		statusLine = fmt.Sprintf("%s %s · %s · %.1fs", m.getBouncingDots(), agentID, modelName, m.elapsedTime.Seconds())
	} else if m.lastDuration > 0 {
		statusLine = fmt.Sprintf("● %s · %s · %.1fs", agentID, modelName, m.lastDuration.Seconds())
	} else {
		statusLine = fmt.Sprintf("● %s · %s · idle", agentID, modelName)
	}

	var autocompleteView string
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		var autoSb strings.Builder
		for i, cmd := range m.autocompleteItems {
			line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
			if i == m.autocompleteIdx {
				autoSb.WriteString(ModalItemActive.Render(line) + "\n")
			} else {
				autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
			}
		}
		autocompleteView = ModalContainer.Width(leftWidth - 4).Render(autoSb.String()) + "\n"
	}

	leftBuilder.WriteString(StatusLineStyle.Render(statusLine) + "\n")
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView)
	}

	m.textInput.Width = leftWidth - 4
	inputBar := InputBarContainer.Width(leftWidth - 2).Render(m.textInput.View())
	leftBuilder.WriteString(inputBar + "\n")

	tokens, limit := m.agentLoop.GetProvidable().GetCurrentContextUsage(m.currentKey)
	pct := 0.0
	if limit > 0 {
		pct = float64(tokens) / float64(limit) * 100
	}
	tokensText := fmt.Sprintf("%d (%.1f%%)", tokens, pct)
	bottomBar := lipgloss.JoinHorizontal(lipgloss.Top,
		BottomBarLeft.Width((leftWidth-2)/2).Render(fmt.Sprintf("%s · %s · %s", agentID, modelName, thinkLevel)),
		BottomBarRight.Width((leftWidth-2)/2).Align(lipgloss.Right).Render(fmt.Sprintf("%s | ctrl+p commands", tokensText)),
	)
	leftBuilder.WriteString(bottomBar)

	leftPane := LeftColumnStyle.Width(leftWidth).Render(leftBuilder.String())

	// Render Right Column (Sidebar Panel)
	var rightBuilder strings.Builder

	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = "Nuevo Chat"
	}
	rightBuilder.WriteString(SidebarTitle.Render(sessionName) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render("Context") + "\n")
	cumInput, cumOutput, limit := m.agentLoop.GetProvidable().GetTokenCounts(m.currentKey)
	cumPct := 0.0
	if limit > 0 {
		cumPct = float64(cumInput) / float64(limit) * 100
	}
	rightBuilder.WriteString(SidebarValue.Render(fmt.Sprintf("%s tokens", formatNumber(cumInput+cumOutput))) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(fmt.Sprintf("%.1f%% used", cumPct)) + "\n")
	rightBuilder.WriteString(SidebarValue.Render("$0.00 spent") + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render("MCP") + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" workspace Connected") + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" system Connected") + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render("LSP") + "\n")
	rightBuilder.WriteString(SidebarValue.Render("LSPs are disabled") + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render("Workspace") + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.workspacePath) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.gitBranch) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render("Status") + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" Lele "+agent.GatewayVersion()) + "\n")

	rightPane := RightSidebar.Width(rightWidth).Height(contentHeight).Render(rightBuilder.String())

	mainLayout := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	if m.modalMode != ModalNone {
		var modalTitle string
		switch m.modalMode {
		case ModalAgent:
			modalTitle = "Selecciona un Agente"
		case ModalModel:
			modalTitle = "Selecciona un Modelo"
		case ModalSessions:
			modalTitle = "Selecciona un Chat"
		}

		var modalSb strings.Builder
		modalSb.WriteString(TitleStyle.Render(modalTitle) + "\n\n")

		for i, item := range m.modalItems {
			if i == m.modalSelectedIdx {
				modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
			} else {
				modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
			}
		}

		modalView := ModalContainer.Render(modalSb.String())
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalView)
	}

	return AppContainer.Width(m.width).Height(m.height).Render(mainLayout)
}

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var res []string
	for len(s) > 3 {
		res = append([]string{s[len(s)-3:]}, res...)
		s = s[:len(s)-3]
	}
	res = append([]string{s}, res...)
	return strings.Join(res, ",")
}

func (m *Model) getBouncingDots() string {
	width := 12
	pos := m.animationTick % (2 * (width - 3))
	var offset int
	if pos < width-3 {
		offset = pos
	} else {
		offset = 2*(width-3) - pos
	}

	var sb strings.Builder
	sb.WriteRune('[')
	for i := 0; i < width; i++ {
		if i == offset || i == offset+1 || i == offset+2 {
			sb.WriteString(lipgloss.NewStyle().Foreground(SecondaryColor).Render("●"))
		} else {
			sb.WriteRune(' ')
		}
	}
	sb.WriteRune(']')
	return sb.String()
}
