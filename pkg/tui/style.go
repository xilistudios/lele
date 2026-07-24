package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	// BgColor and other Dracula / Terminal-based premium color palette colors.
	BgColor        = lipgloss.Color("#181824") // Very dark gray-blue background
	InputBgColor   = lipgloss.Color("#212130") // Slightly lighter input background
	PrimaryColor   = lipgloss.Color("#FF5555") // Dracula Red
	SecondaryColor = lipgloss.Color("#50FA7B") // Dracula Green
	AccentColor    = lipgloss.Color("#8BE9FD") // Dracula Cyan
	PurpleColor    = lipgloss.Color("#BD93F9") // Dracula Purple
	OrangeColor    = lipgloss.Color("#FFB86C") // Dracula Orange
	CommentColor   = lipgloss.Color("#6272A4") // Dracula Comment / Muted gray
	Foreground     = lipgloss.Color("#F8F8F2") // Dracula Foreground
	SelectionBg    = lipgloss.Color("#44475A") // Dracula Selection background
	YellowColor    = lipgloss.Color("#F1FA8C") // Dracula Yellow

	// AppContainer is the main container style.
	AppContainer = lipgloss.NewStyle().
			Background(BgColor).
			Foreground(Foreground)

	StatusRunning   = lipgloss.NewStyle().Foreground(YellowColor)
	StatusCompleted = lipgloss.NewStyle().Foreground(SecondaryColor)
	StatusFailed    = lipgloss.NewStyle().Foreground(PrimaryColor)

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
				Foreground(lipgloss.Color("#FF5555"))

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

	// ApprovalRejected styles the "rejected" result message.
	ApprovalRejected = lipgloss.NewStyle().
				Foreground(PrimaryColor).
				Bold(true)

	// Group turn styles for Mixture of Agents rendering.
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
)

func SidebarLabelValue(label, value string) string {
	labelPart := SidebarLabel.Render(fmt.Sprintf("%-18s", label))
	valuePart := SidebarValue.Render(value)
	return labelPart + valuePart
}
