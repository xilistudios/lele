package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type mcpAddResultMsg struct {
	name     string
	layer    string
	path     string
	created  bool
	replaced bool
	changed  bool
	shadow   string
	err      error
}

func (m *Model) mcpAddCmd(layer, name string, entry mcp.ServerConfig) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpAddResultMsg{name: name, layer: layer, err: errMCPUnavailable}
		}
	}
	return mcpAddCmdFor(paths, layer, name, entry)
}

func mcpAddCmdFor(paths mcp.Paths, layer, name string, entry mcp.ServerConfig) tea.Cmd {
	return func() tea.Msg {
		file := mcp.LayerFile(paths, layer)
		if file == "" {
			return mcpAddResultMsg{name: name, layer: layer, err: errMCPOwnerNoPath}
		}
		if filepath.Base(file) != "mcp.json" {
			return mcpAddResultMsg{name: name, layer: layer, path: file, err: errMCPPathNotAllowed}
		}
		if !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
			return mcpAddResultMsg{name: name, layer: layer, path: file, err: errMCPPathNotAllowed}
		}
		inv := mcp.ReadInventory(paths)
		shadow := mcpAddShadowLayer(inv, layer, name)
		res, err := mcp.UpsertEntry(file, name, entry)
		if err != nil {
			return mcpAddResultMsg{name: name, layer: layer, path: file, shadow: shadow, err: err}
		}
		return mcpAddResultMsg{
			name: name, layer: layer, path: file,
			created: res.Created, replaced: res.Replaced, changed: res.Changed,
			shadow: shadow,
		}
	}
}

func mcpAddLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

func mcpAddShadowLayer(inv mcp.Inventory, layer, name string) string {
	rank := mcpAddLayerRank(layer)
	best, bestRank := "", -1
	for _, st := range inv.ByName[name] {
		r := mcpAddLayerRank(st.Layer)
		if r <= rank || r <= bestRank {
			continue
		}
		best, bestRank = st.Layer, r
	}
	return best
}

func mcpAddErrorKind(err error) string {
	if err == nil {
		return ""
	}
	msg := sanitizeDisplayText(err.Error())
	switch {
	case !strings.HasPrefix(msg, "mcp: "):
		kind, _, _ := strings.Cut(msg, " ")
		return kind
	default:
		rest := strings.TrimPrefix(msg, "mcp: ")
		verb, _, _ := strings.Cut(rest, " ")
		return verb
	}
}

func (m *Model) handleMCPAddResult(msg mcpAddResultMsg) tea.Cmd {
	switch {
	case msg.err == nil:
		switch {
		case msg.created:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddSuccess"), msg.name, msg.layer, msg.path)
		case msg.replaced:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddReplaced"), msg.name, msg.layer, msg.path)
		default:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddNoChange"), msg.name, msg.layer)
		}
		if msg.shadow != "" {
			m.mcpFeedback += " " + fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), msg.shadow)
		}
		syncMCPServersFor(m.agentLoop)
		if want := mcpFilterLayer(m.mcpFilter); want != "" && want != msg.layer {
			m.mcpFilter = mcpFilterAll
		}
	case errors.Is(msg.err, errMCPUnavailable):
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
	case errors.Is(msg.err, errMCPOwnerNoPath):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddLayerNoPath"), msg.layer)
	case errors.Is(msg.err, errMCPPathNotAllowed):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddPathNotAllowed"), msg.name, msg.path)
	default:
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddFailed"), msg.name, mcpAddErrorKind(msg.err))
	}
	m.loadMCPList()
	if msg.err == nil {
		m.reselectMCPRow(msg.name)
	}
	return m.tickCmd()
}

func (m *Model) reselectMCPRow(name string) {
	if name == "" {
		return
	}
	for i, k := range m.mcpModalKeys {
		if k == name {
			m.modalSelectedIdx = i
			m.clampModalCursor()
			return
		}
	}
	m.clampModalCursor()
}

func (m *Model) startMCPAdd() tea.Cmd {
	if _, ok := m.mcpPaths(); !ok {
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
		return nil
	}
	return m.openMCPAddWizard()
}

func (m *Model) openMCPAddWizard() tea.Cmd {
	m.modalMode = ModalMCPAdd
	m.formStepIndex = mcpAddStepName
	m.formValues = make([]string, mcpAddStepCount)
	m.formError = ""
	m.formConfirmMode = false
	m.mcpFeedback = ""
	m.modalScrollOffset = 0

	m.textInput.SetValue("")
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(mcpAddStepName)
	m.syncTextInputEcho()
	return m.textInput.Focus()
}

func mcpAddNextSlot(kind string, pos int) int {
	slots := mcpAddVisibleSlots(kind)
	if pos < 0 || pos+1 >= len(slots) {
		return -1
	}
	return slots[pos+1]
}

func mcpAddPosition(kind string, slot int) int {
	slots := mcpAddVisibleSlots(kind)
	for i, s := range slots {
		if s == slot {
			return i
		}
	}
	return -1
}

func (m *Model) advanceMCPAdd(val string) tea.Cmd {
	kind := m.mcpAddTransport()

	pos := mcpAddPosition(kind, m.formStepIndex)
	if pos < 0 {
		m.formStepIndex = mcpAddVisibleSlots(kind)[0]
		m.textInput.SetValue(m.formValue(m.formStepIndex))
		m.textInput.Placeholder = m.mcpAddStepPlaceholder(m.formStepIndex)
		return nil
	}
	slot := m.formStepIndex

	if slot == mcpAddStepReview {
		switch yn, ok := parseWizardYesNo(val); {
		case !ok || yn == "":
			m.formError = i18n.T("tui.mcpAddErrConfirm")
			return nil
		case yn == "no":
			m.exitMCPAddWizard()
			return nil
		default:
			entry, err := m.mcpAddEntry()
			if err != nil {
				return m.mcpAddBackToSlot(err.Error())
			}
			layer := m.formValue(mcpAddStepLayer)
			name := strings.TrimSpace(m.formValue(mcpAddStepName))
			m.exitMCPAddWizard()
			return m.mcpAddCmd(layer, name, entry)
		}
	}

	switch slot {
	case mcpAddStepName:
		name, err := mcpAddValidateName(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = name
	case mcpAddStepKind:
		kindVal, err := mcpAddParseTransport(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = kindVal
	case mcpAddStepEndpoint:
		endpoint, err := mcpAddParseEndpoint(m.mcpAddTransport(), val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = endpoint
	case mcpAddStepType:
		typ, err := mcpAddParseType(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = typ
	case mcpAddStepEnv, mcpAddStepHeaders:
		if _, err := mcpAddParsePairs(val); err != nil {
			m.formError = err.Error()
			return nil
		}
	case mcpAddStepLayer:
		layer, err := mcpAddParseLayer(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		paths, ok := m.mcpPaths()
		if !ok {
			m.formError = i18n.T("tui.mcpUnavailable")
			return nil
		}
		if file := mcp.LayerFile(paths, layer); file == "" {
			m.formError = fmt.Sprintf(i18n.T("tui.mcpAddErrLayerNoPath"), layer)
			return nil
		} else if !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
			m.formError = fmt.Sprintf(i18n.T("tui.mcpAddErrPathNotAllowed"), layer, file)
			return nil
		}
		val = layer
	}

	m.formError = ""
	if slot < len(m.formValues) {
		m.formValues[slot] = val
	}

	nextKind := m.mcpAddTransport()
	nextSlot := mcpAddNextSlot(nextKind, mcpAddPosition(nextKind, slot))
	if nextSlot < 0 {
		return nil
	}
	m.formStepIndex = nextSlot
	m.textInput.SetValue(m.formValue(m.formStepIndex))
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(m.formStepIndex)
	return m.textInput.Focus()
}

func (m *Model) mcpAddBackToSlot(msg string) tea.Cmd {
	m.formError = msg
	m.formStepIndex = mcpAddStepName
	m.textInput.SetValue(m.formValue(mcpAddStepName))
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(mcpAddStepName)
	return m.textInput.Focus()
}

func (m *Model) exitMCPAddWizard() {
	m.modalMode = ModalMCP
	m.formValues = nil
	m.formStepIndex = 0
	m.formError = ""
	m.formConfirmMode = false
	m.textInput.SetValue("")
	m.textInput.Placeholder = ""
	m.syncTextInputEcho()
}

func (m *Model) handleMCPAddKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "enter":
		return m.advanceMCPAdd(strings.TrimSpace(m.textInput.Value())), true
	case "esc":
		m.exitMCPAddWizard()
		return nil, true
	}
	return nil, false
}
