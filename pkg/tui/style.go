package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

var (
	// BgColor and other Dracula / Terminal-based premium color palette colors.
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

	// AppContainer is the main container style.
	AppContainer lipgloss.Style

	StatusRunning   lipgloss.Style
	StatusCompleted lipgloss.Style
	StatusFailed    lipgloss.Style

	// BouncingDot is the styled dot used by the loading animation. It is a
	// package-level style because the animation runs ~10 times per second and
	// allocating a new lipgloss style per dot per frame was measurable overhead.
	BouncingDot     lipgloss.Style
	bouncingDotChar string

	// RightSidebar styles for the right column.
	RightSidebar lipgloss.Style

	SidebarTitle  lipgloss.Style
	SidebarHeader lipgloss.Style
	SidebarValue  lipgloss.Style
	SidebarLabel  lipgloss.Style

	SidebarConnectedDot lipgloss.Style
	SidebarDisabledDot  lipgloss.Style

	// LeftColumnStyle is the left chat column layout.
	LeftColumnStyle lipgloss.Style

	// HeaderStyle is the top header style.
	HeaderStyle lipgloss.Style

	// ViewportStyle is the chat history viewport.
	ViewportStyle lipgloss.Style

	// InputBarContainer is the text input box (full-width bar, no borders, highlighted background).
	InputBarContainer lipgloss.Style

	// StatusLineStyle is the active status / duration line.
	StatusLineStyle lipgloss.Style

	// BottomBarLeft is the left side of the bottom bar.
	BottomBarLeft lipgloss.Style

	BottomBarRight lipgloss.Style

	// UserRoleStyle styles user messages.
	UserRoleStyle lipgloss.Style

	UserMessageStyle lipgloss.Style

	AssistantRoleStyle lipgloss.Style

	AssistantMessageStyle lipgloss.Style

	SystemRoleStyle lipgloss.Style

	SystemMessageStyle lipgloss.Style

	ThinkingLabelStyle lipgloss.Style

	ThinkingContentStyle lipgloss.Style

	// ToolCallLabel styles tool call labels.
	ToolCallLabel lipgloss.Style

	ToolCallName lipgloss.Style

	ToolCallBox lipgloss.Style

	ToolResultLabel lipgloss.Style

	ToolResultBox lipgloss.Style

	// SubagentProgressLabel displays real-time subagent progress in parent chat.
	SubagentProgressLabel lipgloss.Style

	SubagentProgressStyle lipgloss.Style

	// WelcomeLogo is the welcome page logo style.
	WelcomeLogo lipgloss.Style

	WelcomeTip lipgloss.Style

	// ModalContainer styles modals and command autocompletes.
	ModalContainer lipgloss.Style

	ModalItemActive   lipgloss.Style
	ModalItemInactive lipgloss.Style

	HelpStyle         lipgloss.Style
	CommentColorStyle lipgloss.Style
	TitleStyle        lipgloss.Style

	// ModelSelectorStyle is the model selector on the welcome screen.
	ModelSelectorStyle lipgloss.Style

	ModelSelectorLabel lipgloss.Style

	// ApprovalBox styles the pending command approval prompt in the viewport.
	ApprovalBox lipgloss.Style

	// ApprovalApproved styles the "approved" result message.
	ApprovalApproved lipgloss.Style

	// SuccessStyle styles success confirmation messages in modals.
	SuccessStyle lipgloss.Style

	// ApprovalRejected styles the "rejected" result message.
	ApprovalRejected lipgloss.Style

	// GroupTurnHeader renders the turn header line (┌ [label · Layer N · role]).
	GroupTurnHeader lipgloss.Style

	// GroupTurnBorder renders the left border for group turn content.
	GroupTurnBorder lipgloss.Style

	// GroupLayerSeparator renders layer separator lines.
	GroupLayerSeparator lipgloss.Style

	// GroupSynthesisLabel renders the synthesis section label.
	GroupSynthesisLabel lipgloss.Style

	// GroupSynthesisBorder renders the border around the final synthesis.
	GroupSynthesisBorder lipgloss.Style
)

// init initializes the package with the Dracula default theme at startup.
func init() {
	rebuildStyles(theme.DraculaDefault)
}

// ApplyTheme re-applies the given theme to all package-level styles. It is
// the public API for live theme switching.
func ApplyTheme(t theme.Theme) {
	rebuildStyles(t)
}

// rebuildStyles (re)assigns every package-level color and style var from the
// given theme. The style expressions mirror the previous inline initializers.
func rebuildStyles(t theme.Theme) {
	BgColor        = lipgloss.Color(t.Background)
	InputBgColor   = lipgloss.Color(t.InputBackground)
	PrimaryColor   = lipgloss.Color(t.Primary)
	SecondaryColor = lipgloss.Color(t.Secondary)
	AccentColor    = lipgloss.Color(t.Accent)
	PurpleColor    = lipgloss.Color(t.Purple)
	OrangeColor    = lipgloss.Color(t.Orange)
	CommentColor   = lipgloss.Color(t.Comment)
	Foreground     = lipgloss.Color(t.Foreground)
	SelectionBg    = lipgloss.Color(t.SelectionBackground)
	YellowColor    = lipgloss.Color(t.Yellow)

	// AppContainer is the main container style.
	AppContainer = lipgloss.NewStyle().
			Background(BgColor).
			Foreground(Foreground)

	StatusRunning   = lipgloss.NewStyle().Foreground(YellowColor)
	StatusCompleted = lipgloss.NewStyle().Foreground(SecondaryColor)
	StatusFailed    = lipgloss.NewStyle().Foreground(PrimaryColor)

	// BouncingDot is the styled dot used by the loading animation. It is a
	// package-level style because the animation runs ~10 times per second and
	// allocating a new lipgloss style per dot per frame was measurable overhead.
	BouncingDot     = lipgloss.NewStyle().Foreground(SecondaryColor)
	bouncingDotChar = BouncingDot.Render("●")

	// RightSidebar styles for the right column.
	RightSidebar = lipgloss.NewStyle().
			Border(lipgloss.Border{Left: "│"}, false, false, false, true).
			BorderForeground(CommentColor).
			PaddingLeft(2).
			PaddingRight(1)

	SidebarTitle = lipgloss.NewStyle().
			Foreground(Foreground).
			Bold(true).
			MarginBottom(1)

	SidebarHeader = lipgloss.NewStyle().
			Foreground(CommentColor).
			Bold(true).
			MarginTop(1).
			MarginBottom(0)

	SidebarValue = lipgloss.NewStyle().
			Foreground(Foreground).
			PaddingLeft(1)

	SidebarLabel = lipgloss.NewStyle().
			Foreground(SecondaryColor)

	SidebarConnectedDot = lipgloss.NewStyle().
				Foreground(SecondaryColor)

	SidebarDisabledDot = lipgloss.NewStyle().
				Foreground(PrimaryColor)

	// LeftColumnStyle is the left chat column layout.
	LeftColumnStyle = lipgloss.NewStyle().
			PaddingRight(1)

	// HeaderStyle is the top header style.
	HeaderStyle = lipgloss.NewStyle().
			Foreground(CommentColor).
			MarginBottom(1)

	// ViewportStyle is the chat history viewport.
	ViewportStyle = lipgloss.NewStyle()

	// InputBarContainer is the text input box (full-width bar, no borders, highlighted background).
	InputBarContainer = lipgloss.NewStyle().
				Background(InputBgColor).
				Padding(0, 1).
				MarginTop(1).
				MarginBottom(1)

	// StatusLineStyle is the active status / duration line.
	StatusLineStyle = lipgloss.NewStyle().
			Foreground(CommentColor).
			MarginTop(1).
			MarginBottom(1)

	// BottomBarLeft is the left side of the bottom bar.
	BottomBarLeft = lipgloss.NewStyle().
			Foreground(CommentColor)

	BottomBarRight = lipgloss.NewStyle().
			Foreground(CommentColor)

	// UserRoleStyle styles user messages.
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

	// ToolCallLabel styles tool call labels.
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

	// SubagentProgressLabel displays real-time subagent progress in parent chat.
	SubagentProgressLabel = lipgloss.NewStyle().
				Foreground(OrangeColor).
				Bold(true)

	SubagentProgressStyle = lipgloss.NewStyle().
				Foreground(OrangeColor).
				Italic(true)

	// WelcomeLogo is the welcome page logo style.
	WelcomeLogo = lipgloss.NewStyle().
			Foreground(PrimaryColor).
			Bold(true)

	WelcomeTip = lipgloss.NewStyle().
			Foreground(CommentColor).
			Italic(true).
			Align(lipgloss.Center)

	// ModalContainer styles modals and command autocompletes.
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

	// ModelSelectorStyle is the model selector on the welcome screen.
	ModelSelectorStyle = lipgloss.NewStyle().
				Foreground(AccentColor).
				Background(SelectionBg).
				Bold(true).
				Padding(0, 1)

	ModelSelectorLabel = lipgloss.NewStyle().
				Foreground(CommentColor)

	// ApprovalBox styles the pending command approval prompt in the viewport.
	ApprovalBox = lipgloss.NewStyle().
			Foreground(YellowColor).
			Border(lipgloss.NormalBorder()).
			BorderForeground(YellowColor).
			Background(InputBgColor).
			Padding(1, 2)

	// ApprovalApproved styles the "approved" result message.
	ApprovalApproved = lipgloss.NewStyle().
				Foreground(SecondaryColor).
				Bold(true)

	// SuccessStyle styles success confirmation messages in modals.
	SuccessStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor).
			Bold(true)

	// ApprovalRejected styles the "rejected" result message.
	ApprovalRejected = lipgloss.NewStyle().
				Foreground(PrimaryColor).
				Bold(true)

	// GroupTurnHeader renders the turn header line (┌ [label · Layer N · role]).
	GroupTurnHeader = lipgloss.NewStyle().
			Foreground(PurpleColor).
			Bold(true)

	// GroupTurnBorder renders the left border for group turn content.
	GroupTurnBorder = lipgloss.NewStyle().
			Foreground(PurpleColor).
			PaddingLeft(1).
			Border(lipgloss.Border{Left: "│"}, false, false, false, true).
			BorderForeground(PurpleColor)

	// GroupLayerSeparator renders layer separator lines.
	GroupLayerSeparator = lipgloss.NewStyle().
				Foreground(PurpleColor).
				Bold(true).
				Align(lipgloss.Center)

	// GroupSynthesisLabel renders the synthesis section label.
	GroupSynthesisLabel = lipgloss.NewStyle().
				Foreground(SecondaryColor).
				Bold(true)

	// GroupSynthesisBorder renders the border around the final synthesis.
	GroupSynthesisBorder = lipgloss.NewStyle().
				Foreground(SecondaryColor).
				PaddingLeft(1).
				Border(lipgloss.Border{Left: "│"}, false, false, false, true).
				BorderForeground(SecondaryColor)

	// SelectionStyle is the highlight applied to selected text in the viewport.
	SelectionStyle = lipgloss.NewStyle().Background(SelectionBg)
}

func SidebarLabelValue(label, value string) string {
	labelPart := SidebarLabel.Render(fmt.Sprintf("%-18s", label))
	valuePart := SidebarValue.Render(value)
	return labelPart + valuePart
}

// paintFrame renders a full-screen frame: the content is placed (centered)
// in the terminal, then the app background is painted over the whole frame
// and re-applied after every inner ANSI reset so no cell is left unpainted.
//
// Every View() exit path MUST return paintFrame(...) so the background is
// uniform across all screens (welcome, chat, modals, detail views).
func (m *Model) paintFrame(content string) string {
	placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	// Place already pads content to exactly m.width x m.height, so Width/Height
	// here would force a redundant re-measure/re-pad of every line (~200k ns on
	// a 200x50 frame). MaxWidth/MaxHeight only clamp oversized content, which is
	// a no-op for the normal (already-fitting) case.
	return reapplyBackground(AppContainer.MaxWidth(m.width).MaxHeight(m.height).Render(placed))
}

// reapplyBackground re-emits the correct background color after every full
// ANSI reset in a rendered frame.
//
// Inner lipgloss styles terminate their text with "\x1b[0m", which cancels
// ALL attributes — including the background set by an enclosing container.
// Any cell printed after such a reset is therefore left unpainted and the
// terminal's own (often translucent) background bleeds through, producing
// visible color bands next to styled content.
//
// The re-emission is context-aware: lipgloss output is well formed (every
// styled run is "OPEN … \x1b[0m" and runs nest lexically), so we track a
// stack of background-setting sequences. After a reset we re-emit the
// innermost still-open background — the app background at top level, and the
// container background (input bar, modal, tool box, …) for cells inside a
// container that owns its own background. Re-emitting the full opening
// sequence (fg+bg) is safe: foreground is invisible on the space cells that
// follow a reset, and raw text inside a container legitimately inherits the
// container colors.
func reapplyBackground(s string) string {
	appSeq := backgroundOpenSeq(BgColor)
	if appSeq == "" {
		return s
	}

	var sb strings.Builder
	sb.Grow(len(s) + 128)

	// Lipgloss output is well formed: every styled run is "OPEN … \x1b[0m"
	// and runs nest lexically. Track the run nesting so each reset knows
	// whether it closes a background-owning run.
	var bgStack []string // opening seqs of open runs that set a background
	var runHasBg []bool  // per open run, whether it set a background
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

		// Attempt to parse an SGR sequence: ESC '[' <digits/semicolons> 'm'.
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
			// Not a valid SGR sequence — skip just the ESC, matching the
			// regex's non-match behavior on that byte.
			i = esc + 1
			continue
		}
		seq := s[esc : k+1]
		params := s[j+1 : k] // bytes between '[' and 'm'

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
			// Foreground-only or attribute sequence: opens a run whose reset
			// must not pop the enclosing background context.
			open(false, seq)
			sb.WriteString(seq)
		}
		last = k + 1
		i = k + 1
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// backgroundOpenSeq returns the opening ANSI sequence for a background color
// under the current color profile (e.g. "\x1b[48;2;24;24;36m"), without a
// trailing reset. Empty when the profile cannot render colors.
func backgroundOpenSeq(c lipgloss.Color) string {
	seq := lipgloss.NewStyle().Background(c).Render("")
	return strings.TrimSuffix(seq, "\x1b[0m")
}

// paramsHasBackground reports whether an SGR parameter list sets a background
// (ANSI 48 = extended background, 40-47 = basic backgrounds).
func paramsHasBackground(params string) bool {
	for _, p := range strings.Split(params, ";") {
		if p == "48" || (len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7') {
			return true
		}
	}
	return false
}

// isFullReset reports whether an SGR parameter list is a full reset.
func isFullReset(params string) bool {
	return params == "0" || params == "00" || params == ""
}