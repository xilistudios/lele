package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

var (
	BgColor        lipgloss.Color
	InputBgColor   lipgloss.Color
	PrimaryColor   lipgloss.Color
	SecondaryColor lipgloss.Color
	AccentColor    lipgloss.Color
	PurpleColor    lipgloss.Color
	OrangeColor    lipgloss.Color
	CommentColor   lipgloss.Color
	Foreground     lipgloss.Color
	SelectionBg    lipgloss.Color
	YellowColor    lipgloss.Color

	AppContainer lipgloss.Style

	StatusRunning   lipgloss.Style
	StatusCompleted lipgloss.Style
	StatusFailed    lipgloss.Style

	BouncingDot     lipgloss.Style
	bouncingDotChar string

	RightSidebar lipgloss.Style

	SidebarTitle  lipgloss.Style
	SidebarHeader lipgloss.Style
	SidebarValue  lipgloss.Style
	SidebarLabel  lipgloss.Style

	SidebarConnectedDot lipgloss.Style
	SidebarDisabledDot  lipgloss.Style

	LeftColumnStyle lipgloss.Style

	HeaderStyle lipgloss.Style

	ViewportStyle lipgloss.Style

	InputBarContainer lipgloss.Style

	StatusLineStyle lipgloss.Style

	QueueRowStyle lipgloss.Style

	BottomBarLeft lipgloss.Style

	BottomBarRight lipgloss.Style

	UserRoleStyle lipgloss.Style

	UserMessageStyle lipgloss.Style

	AssistantRoleStyle lipgloss.Style

	AssistantMessageStyle lipgloss.Style

	SystemRoleStyle lipgloss.Style

	SystemMessageStyle lipgloss.Style

	ThinkingLabelStyle lipgloss.Style

	ThinkingContentStyle lipgloss.Style

	ToolCallLabel lipgloss.Style

	ToolCallName lipgloss.Style

	ToolCallBox lipgloss.Style

	ToolResultLabel lipgloss.Style

	ToolResultBox lipgloss.Style

	ErrorTitle lipgloss.Style

	ErrorBox lipgloss.Style

	SubagentProgressLabel lipgloss.Style

	SubagentProgressStyle lipgloss.Style

	WelcomeLogo lipgloss.Style

	WelcomeTip lipgloss.Style

	ModalContainer lipgloss.Style

	ModalItemActive   lipgloss.Style
	ModalItemInactive lipgloss.Style

	HelpStyle         lipgloss.Style
	CommentColorStyle lipgloss.Style
	TitleStyle        lipgloss.Style

	ModelSelectorStyle lipgloss.Style

	ModelSelectorLabel lipgloss.Style

	ApprovalBox lipgloss.Style

	ApprovalApproved lipgloss.Style

	SuccessStyle lipgloss.Style

	ApprovalRejected lipgloss.Style

	GroupTurnHeader lipgloss.Style

	GroupTurnBorder lipgloss.Style

	GroupLayerSeparator lipgloss.Style

	GroupSynthesisLabel lipgloss.Style

	GroupSynthesisBorder lipgloss.Style
)

func init() {
	rebuildStyles(theme.DraculaDefault)
}

func ApplyTheme(t theme.Theme) {
	rebuildStyles(t)
}

func rebuildStyles(t theme.Theme) {
	BgColor = lipgloss.Color(t.Background)
	InputBgColor = lipgloss.Color(t.InputBackground)
	PrimaryColor = lipgloss.Color(t.Primary)
	SecondaryColor = lipgloss.Color(t.Secondary)
	AccentColor = lipgloss.Color(t.Accent)
	PurpleColor = lipgloss.Color(t.Purple)
	OrangeColor = lipgloss.Color(t.Orange)
	CommentColor = lipgloss.Color(t.Comment)
	Foreground = lipgloss.Color(t.Foreground)
	SelectionBg = lipgloss.Color(t.SelectionBackground)
	YellowColor = lipgloss.Color(t.Yellow)

	AppContainer = lipgloss.NewStyle().
		Background(BgColor).
		Foreground(Foreground)

	StatusRunning = lipgloss.NewStyle().Foreground(YellowColor)
	StatusCompleted = lipgloss.NewStyle().Foreground(SecondaryColor)
	StatusFailed = lipgloss.NewStyle().Foreground(PrimaryColor)

	BouncingDot = lipgloss.NewStyle().Foreground(SecondaryColor)
	bouncingDotChar = BouncingDot.Render("●")

	RightSidebar = lipgloss.NewStyle().
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(CommentColor).
		PaddingLeft(2).
		PaddingRight(1)

	SidebarTitle = lipgloss.NewStyle().
		Foreground(Foreground).
		Bold(true)

	SidebarHeader = lipgloss.NewStyle().
		Foreground(CommentColor).
		Bold(true)

	SidebarValue = lipgloss.NewStyle().
		Foreground(Foreground).
		PaddingLeft(1)

	SidebarLabel = lipgloss.NewStyle().
		Foreground(SecondaryColor)

	SidebarConnectedDot = lipgloss.NewStyle().
		Foreground(SecondaryColor)

	SidebarDisabledDot = lipgloss.NewStyle().
		Foreground(PrimaryColor)

	LeftColumnStyle = lipgloss.NewStyle().
		PaddingRight(1)

	HeaderStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		MarginBottom(1)

	ViewportStyle = lipgloss.NewStyle()

	InputBarContainer = lipgloss.NewStyle().
		Background(InputBgColor).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	StatusLineStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		MarginTop(1).
		MarginBottom(1)

	QueueRowStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		Italic(true)

	BottomBarLeft = lipgloss.NewStyle().
		Foreground(CommentColor)

	BottomBarRight = lipgloss.NewStyle().
		Foreground(CommentColor)

	UserRoleStyle = lipgloss.NewStyle().
		Foreground(AccentColor).
		Bold(true)

	UserMessageStyle = lipgloss.NewStyle().
		Foreground(Foreground)

	AssistantRoleStyle = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true)

	AssistantMessageStyle = lipgloss.NewStyle().
		Foreground(Foreground)

	SystemRoleStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		Bold(true)

	SystemMessageStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		Italic(true)

	ThinkingLabelStyle = lipgloss.NewStyle().
		Foreground(OrangeColor).
		Bold(true)

	ThinkingContentStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		Italic(true).
		PaddingLeft(2).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(CommentColor)

	ToolCallLabel = lipgloss.NewStyle().
		Foreground(OrangeColor).
		Bold(true)

	ToolCallName = lipgloss.NewStyle().
		Foreground(PurpleColor).
		Bold(true)

	ToolCallBox = lipgloss.NewStyle().
		Foreground(CommentColor).
		Background(InputBgColor).
		Padding(0, 1).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(OrangeColor)

	ToolResultLabel = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true)

	ToolResultBox = lipgloss.NewStyle().
		Foreground(CommentColor).
		Background(InputBgColor).
		Padding(0, 1).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(SecondaryColor)

	ErrorTitle = lipgloss.NewStyle().
		Foreground(PrimaryColor).
		Bold(true)

	ErrorBox = lipgloss.NewStyle().
		Foreground(PrimaryColor).
		Background(InputBgColor).
		Padding(0, 1).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(PrimaryColor)

	SubagentProgressLabel = lipgloss.NewStyle().
		Foreground(OrangeColor).
		Bold(true)

	SubagentProgressStyle = lipgloss.NewStyle().
		Foreground(OrangeColor).
		Italic(true)

	WelcomeLogo = lipgloss.NewStyle().
		Foreground(PrimaryColor).
		Bold(true)

	WelcomeTip = lipgloss.NewStyle().
		Foreground(CommentColor).
		Italic(true).
		Align(lipgloss.Center)

	ModalContainer = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(PurpleColor).
		Background(InputBgColor).
		Padding(1, 2)

	ModalItemActive = lipgloss.NewStyle().
		Background(SelectionBg).
		Foreground(SecondaryColor).
		Bold(true).
		Padding(0, 1)

	ModalItemInactive = lipgloss.NewStyle().
		Foreground(Foreground).
		Padding(0, 1)

	HelpStyle = lipgloss.NewStyle().
		Foreground(CommentColor)

	CommentColorStyle = lipgloss.NewStyle().
		Foreground(CommentColor).
		Italic(true)

	TitleStyle = lipgloss.NewStyle().
		Foreground(PurpleColor).
		Bold(true)

	ModelSelectorStyle = lipgloss.NewStyle().
		Foreground(AccentColor).
		Background(SelectionBg).
		Bold(true).
		Padding(0, 1)

	ModelSelectorLabel = lipgloss.NewStyle().
		Foreground(CommentColor)

	ApprovalBox = lipgloss.NewStyle().
		Foreground(YellowColor).
		Border(lipgloss.NormalBorder()).
		BorderForeground(YellowColor).
		Background(InputBgColor).
		Padding(1, 2)

	ApprovalApproved = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true)

	SuccessStyle = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true)

	ApprovalRejected = lipgloss.NewStyle().
		Foreground(PrimaryColor).
		Bold(true)

	GroupTurnHeader = lipgloss.NewStyle().
		Foreground(PurpleColor).
		Bold(true)

	GroupTurnBorder = lipgloss.NewStyle().
		Foreground(PurpleColor).
		PaddingLeft(1).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(PurpleColor)

	GroupLayerSeparator = lipgloss.NewStyle().
		Foreground(PurpleColor).
		Bold(true).
		Align(lipgloss.Center)

	GroupSynthesisLabel = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true)

	GroupSynthesisBorder = lipgloss.NewStyle().
		Foreground(SecondaryColor).
		PaddingLeft(1).
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(SecondaryColor)

	SelectionStyle = lipgloss.NewStyle().Background(SelectionBg)
}

const sidebarLabelWidth = 19

func SidebarLabelValue(label, value string) string {
	if w := lipgloss.Width(label); w > sidebarLabelWidth {
		label = truncateRightCells(label, sidebarLabelWidth)
	} else if pad := sidebarLabelWidth - w; pad > 0 {
		label += strings.Repeat(" ", pad)
	}
	labelPart := SidebarLabel.Render(label)
	valuePart := SidebarValue.Render(value)
	return labelPart + valuePart
}

func clampSidebarRow(row string, contentWidth int) string {
	if contentWidth > 0 && lipgloss.Width(row) > contentWidth {
		return truncateRightCells(row, contentWidth)
	}
	return row
}

func (m *Model) paintFrame(content string) string {
	placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	return reapplyBackground(AppContainer.MaxWidth(m.width).MaxHeight(m.height).Render(placed))
}

func reapplyBackground(s string) string {
	appSeq := backgroundOpenSeq(BgColor)
	if appSeq == "" {
		return s
	}

	var sb strings.Builder
	sb.Grow(len(s) + 128)

	var bgStack []string
	var runHasBg []bool
	open := func(hasBg bool, seq string) {
		runHasBg = append(runHasBg, hasBg)
		if hasBg {
			bgStack = append(bgStack, seq)
		}
	}
	closeRun := func() {
		if n := len(runHasBg); n > 0 {
			if runHasBg[n-1] {
				bgStack = bgStack[:len(bgStack)-1]
			}
			runHasBg = runHasBg[:n-1]
		}
	}

	last := 0
	n := len(s)
	for i := 0; i < n; {
		esc := strings.IndexByte(s[i:], 0x1b)
		if esc < 0 {
			break
		}
		esc += i

		j := esc + 1
		if j >= n || s[j] != '[' {
			i = esc + 1
			continue
		}
		k := j + 1
		for k < n && (s[k] == ';' || (s[k] >= '0' && s[k] <= '9')) {
			k++
		}
		if k >= n || s[k] != 'm' {
			i = esc + 1
			continue
		}
		seq := s[esc : k+1]
		params := s[j+1 : k]

		sb.WriteString(s[last:esc])

		switch {
		case paramsHasBackground(params):
			open(true, seq)
			sb.WriteString(seq)
		case isFullReset(params):
			closeRun()
			sb.WriteString(seq)
			top := appSeq
			if len(bgStack) > 0 {
				top = bgStack[len(bgStack)-1]
			}
			sb.WriteString(top)
		default:
			open(false, seq)
			sb.WriteString(seq)
		}
		last = k + 1
		i = k + 1
	}
	sb.WriteString(s[last:])
	return sb.String()
}

func backgroundOpenSeq(c lipgloss.Color) string {
	seq := lipgloss.NewStyle().Background(c).Render("")
	return strings.TrimSuffix(seq, "\x1b[0m")
}

func paramsHasBackground(params string) bool {
	for _, p := range strings.Split(params, ";") {
		if p == "48" || (len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7') {
			return true
		}
	}
	return false
}

func isFullReset(params string) bool {
	return params == "0" || params == "00" || params == ""
}
