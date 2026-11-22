package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	mcpAddStepName = iota
	mcpAddStepKind
	mcpAddStepEndpoint
	mcpAddStepArgs
	mcpAddStepType
	mcpAddStepEnv
	mcpAddStepHeaders
	mcpAddStepDescription
	mcpAddStepLayer
	mcpAddStepReview
	mcpAddStepCount
)

func mcpAddVisibleSlots(kind string) []int {
	switch kind {
	case "stdio":
		return []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepArgs, mcpAddStepEnv, mcpAddStepDescription,
			mcpAddStepLayer, mcpAddStepReview}
	case "remote":
		return []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepType, mcpAddStepEnv, mcpAddStepHeaders,
			mcpAddStepDescription, mcpAddStepLayer, mcpAddStepReview}
	default:
		return []int{mcpAddStepName, mcpAddStepKind}
	}
}

func (m *Model) mcpAddTransport() string {
	if len(m.formValues) <= mcpAddStepKind {
		return ""
	}
	switch m.formValues[mcpAddStepKind] {
	case "stdio", "remote":
		return m.formValues[mcpAddStepKind]
	}
	return ""
}

func mcpAddStepLabel(slot int) string {
	switch slot {
	case mcpAddStepName:
		return i18n.T("tui.mcpAddStepName")
	case mcpAddStepKind:
		return i18n.T("tui.mcpAddStepKind")
	case mcpAddStepEndpoint:
		return i18n.T("tui.mcpAddStepCommand")
	case mcpAddStepArgs:
		return i18n.T("tui.mcpAddStepArgs")
	case mcpAddStepType:
		return i18n.T("tui.mcpAddStepType")
	case mcpAddStepEnv:
		return i18n.T("tui.mcpAddStepEnv")
	case mcpAddStepHeaders:
		return i18n.T("tui.mcpAddStepHeaders")
	case mcpAddStepDescription:
		return i18n.T("tui.mcpAddStepDescription")
	case mcpAddStepLayer:
		return i18n.T("tui.mcpAddStepLayer")
	case mcpAddStepReview:
		return i18n.T("tui.mcpAddStepReview")
	}
	return ""
}

func (m *Model) mcpAddEndpointLabel() string {
	if m.mcpAddTransport() == "remote" {
		return i18n.T("tui.mcpAddStepURL")
	}
	return i18n.T("tui.mcpAddStepCommand")
}

func (m *Model) mcpAddStepPlaceholder(slot int) string {
	switch slot {
	case mcpAddStepName:
		return i18n.T("tui.mcpAddNamePlaceholder")
	case mcpAddStepKind:
		return i18n.T("tui.mcpAddKindPlaceholder")
	case mcpAddStepEndpoint:
		if m.mcpAddTransport() == "remote" {
			return i18n.T("tui.mcpAddURLPlaceholder")
		}
		return i18n.T("tui.mcpAddCommandPlaceholder")
	case mcpAddStepArgs:
		return i18n.T("tui.mcpAddArgsPlaceholder")
	case mcpAddStepType:
		return i18n.T("tui.mcpAddTypePlaceholder")
	case mcpAddStepEnv:
		return i18n.T("tui.mcpAddEnvPlaceholder")
	case mcpAddStepHeaders:
		return i18n.T("tui.mcpAddHeadersPlaceholder")
	case mcpAddStepDescription:
		return i18n.T("tui.mcpAddDescriptionPlaceholder")
	case mcpAddStepLayer:
		return i18n.T("tui.mcpAddLayerPlaceholder")
	case mcpAddStepReview:
		return i18n.T("tui.mcpAddConfirmPlaceholder")
	}
	return ""
}

func (m *Model) mcpAddStepNames() []string {
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	names := make([]string, 0, len(slots))
	for _, slot := range slots {
		label := mcpAddStepLabel(slot)
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		names = append(names, label)
	}
	return names
}

func mcpAddValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrNameRequired"))
	case strings.Contains(name, "/"):
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrNameSlash"))
	}
	return name, nil
}

func mcpAddParseTransport(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "stdio", "s", "1":
		return "stdio", nil
	case "remote", "r", "2":
		return "remote", nil
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrKind"))
}

func mcpAddParseEndpoint(kind, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if kind == "remote" {
			return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrURL"))
		}
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrCommand"))
	}
	return v, nil
}

func mcpAddParseArgs(raw string) []string {
	var out []string
	for _, seg := range strings.Split(raw, ",") {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func mcpAddParseType(raw string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "":
		return "", nil
	case "http", "sse":
		return v, nil
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrType"))
}

func mcpAddParsePairs(raw string) (map[string]string, error) {
	var out map[string]string
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		eq := strings.IndexByte(seg, '=')
		if eq < 0 {
			return nil, fmt.Errorf("%s", i18n.T("tui.mcpAddErrPairNoEquals"))
		}
		key := strings.TrimSpace(seg[:eq])
		if key == "" {
			return nil, fmt.Errorf("%s", i18n.T("tui.mcpAddErrPairNoKey"))
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[key] = strings.TrimSpace(seg[eq+1:])
	}
	return out, nil
}

func mcpMaskPairs(raw string) string {
	segs := strings.Split(raw, ",")
	for i, seg := range segs {
		eq := strings.IndexByte(seg, '=')
		if eq < 0 || eq == len(seg)-1 {
			continue
		}
		prefix := seg[:eq+1]
		suffix := seg[eq+1:]
		trimEnd := strings.TrimRight(suffix, " \t")
		segs[i] = prefix + strings.Repeat("•", 5) + suffix[len(trimEnd):]
	}
	return strings.Join(segs, ",")
}

func mcpAddParseLayer(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return mcp.LayerGlobal, nil
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject:
		return v, nil
	}
	for _, layer := range mcp.Layers() {
		switch layer {
		case mcp.LayerGlobal:
			if v == strings.ToLower(i18n.T("tui.mcpFilterGlobal")) {
				return layer, nil
			}
		case mcp.LayerAgent:
			if v == strings.ToLower(i18n.T("tui.mcpFilterAgent")) {
				return layer, nil
			}
		case mcp.LayerProject:
			if v == strings.ToLower(i18n.T("tui.mcpFilterProject")) {
				return layer, nil
			}
		}
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrLayer"))
}

func (m *Model) mcpAddEntry() (mcp.ServerConfig, error) {
	val := func(slot int) string {
		if slot < len(m.formValues) {
			return m.formValues[slot]
		}
		return ""
	}

	var cfg mcp.ServerConfig

	env, err := mcpAddParsePairs(val(mcpAddStepEnv))
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	cfg.Env = env

	switch m.mcpAddTransport() {
	case "remote":
		endpoint, err := mcpAddParseEndpoint("remote", val(mcpAddStepEndpoint))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		typ, err := mcpAddParseType(val(mcpAddStepType))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		headers, err := mcpAddParsePairs(val(mcpAddStepHeaders))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		cfg.URL = endpoint
		cfg.Type = typ
		cfg.Headers = headers
	case "stdio":
		endpoint, err := mcpAddParseEndpoint("stdio", val(mcpAddStepEndpoint))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		cfg.Command = endpoint
		cfg.Args = mcpAddParseArgs(val(mcpAddStepArgs))
	default:
		return mcp.ServerConfig{}, fmt.Errorf("%s", i18n.T("tui.mcpAddErrKind"))
	}

	cfg.Description = strings.TrimSpace(val(mcpAddStepDescription))
	return cfg, nil
}

func (m *Model) mcpAddReviewWarnings() []string {
	if !m.mcpInventoryValid {
		return nil
	}
	name := strings.TrimSpace(m.formValue(mcpAddStepName))
	if name == "" {
		return nil
	}
	target := mcp.LayerGlobal
	if layer, err := mcpAddParseLayer(m.formValue(mcpAddStepLayer)); err == nil {
		target = layer
	}
	rank := mcpAddLayerRank(target)
	if rank < 0 {
		return nil
	}

	var warnings []string
	for _, row := range m.mcpInventory.ByName[name] {
		rowRank := mcpAddLayerRank(row.Layer)
		if rowRank < 0 {
			continue
		}
		switch {
		case row.Layer == target:
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), name, target))
		case rowRank > rank:
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnShadowed"), row.Layer))
		case rowRank < rank:
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewNoteShadows"), row.Layer))
		}
	}
	return warnings
}

func (m *Model) formValue(slot int) string {
	if slot < len(m.formValues) {
		return m.formValues[slot]
	}
	return ""
}

func (m *Model) renderMCPAddModal() string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(i18n.T("tui.mcpAdd")) + "\n\n")
	sb.WriteString(m.renderMCPAddSteps())
	sb.WriteString("\n")

	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n\n")
	}

	if m.mcpAddCurrentSlot() == mcpAddStepReview {
		sb.WriteString(m.renderMCPAddReview())
	} else {
		m.textInput.Width = 40
		sb.WriteString(InputBarContainer.Width(44).Render(m.textInputView()) + "\n\n")
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAddHints")) + "\n")
	}

	return m.paintFrame(ModalContainer.Render(sb.String()))
}

func (m *Model) renderMCPAddSteps() string {
	var sb strings.Builder
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	current := m.mcpAddCurrentSlot()
	for i := 0; i < len(slots); i++ {
		slot := slots[i]
		label := mcpAddStepLabel(slot)
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		switch {
		case slot < current:
			val := m.mcpAddStepDisplayValue(slot)
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", label, val)) + "\n")
		case slot == current:
			val := m.textInput.Value()
			if slot == mcpAddStepEnv || slot == mcpAddStepHeaders {
				val = mcpMaskPairs(val)
			}
			if val == "" {
				val = "…"
			}
			sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s: [%s]", label, val)) + "\n")
		default:
			sb.WriteString(CommentColorStyle.Render("  ○ "+label) + "\n")
		}
	}
	return sb.String()
}

func (m *Model) mcpAddStepDisplayValue(slot int) string {
	raw := m.formValue(slot)
	var val string
	switch slot {
	case mcpAddStepEnv, mcpAddStepHeaders:
		val = mcpMaskPairs(raw)
	case mcpAddStepArgs:
		if n := len(mcpAddParseArgs(raw)); n > 0 {
			val = fmt.Sprintf("%d", n)
		}
	default:
		val = raw
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return i18n.T("tui.mcpDetailNone")
	}
	return val
}

func (m *Model) renderMCPAddReview() string {
	var sb strings.Builder
	for _, slot := range mcpAddVisibleSlots(m.mcpAddTransport()) {
		if slot == mcpAddStepReview || slot == mcpAddStepKind {
			continue
		}
		label := mcpAddStepLabel(slot)
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", label, m.mcpAddStepDisplayValue(slot))) + "\n")
	}
	for _, w := range m.mcpAddReviewWarnings() {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  "+w) + "\n")
	}
	sb.WriteString("\n" + CommentColorStyle.Render("  "+i18n.T("tui.mcpAddReviewAuthHint")) + "\n")
	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.mcpAddReviewHints")) + "\n")
	return sb.String()
}

func (m *Model) mcpAddCurrentSlot() int {
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	last := slots[len(slots)-1]
	for _, slot := range slots {
		if slot == m.formStepIndex {
			return slot
		}
	}
	if m.formStepIndex < slots[0] {
		return slots[0]
	}
	return last
}
