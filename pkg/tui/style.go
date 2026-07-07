package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Dracula / Terminal-based premium color palette
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

	// Main container styles
	AppContainer = lipgloss.NewStyle().
			Background(BgColor).
			Foreground(Foreground)

	// Sidebar styles (Right Column)
	RightSidebar = lipgloss.NewStyle().
			Border(lipgloss.Border{Left: "│"}, false, false, false, true).
			BorderForeground(SelectionBg).
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

	// Left Chat Column layout
	LeftColumnStyle = lipgloss.NewStyle().
			PaddingRight(1)

	// Top Header
	HeaderStyle = lipgloss.NewStyle().
			Foreground(CommentColor).
			MarginBottom(1)

	// Chat history viewport
	ViewportStyle = lipgloss.NewStyle()

	// Text input box (full-width bar, no borders, highlighted background)
	InputBarContainer = lipgloss.NewStyle().
				Background(InputBgColor).
				Padding(0, 1).
				MarginTop(1).
				MarginBottom(1)

	// Active status / duration line
	StatusLineStyle = lipgloss.NewStyle().
			Foreground(CommentColor).
			MarginTop(1).
			MarginBottom(1)

	// Bottom Bar
	BottomBarLeft = lipgloss.NewStyle().
			Foreground(CommentColor)

	BottomBarRight = lipgloss.NewStyle().
			Foreground(CommentColor)

	// Message styling
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

	// Tool call / tool result styles
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

	// Subagent real-time progress display in parent chat
	SubagentProgressLabel = lipgloss.NewStyle().
				Foreground(OrangeColor).
				Bold(true)

	SubagentProgressStyle = lipgloss.NewStyle().
				Foreground(OrangeColor).
				Italic(true)

	// Welcome Page
	WelcomeLogo = lipgloss.NewStyle().
			Foreground(PrimaryColor).
			Bold(true)

	WelcomeTip = lipgloss.NewStyle().
			Foreground(CommentColor).
			Italic(true).
			Align(lipgloss.Center)

	// Modals / Command autocompletes
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

	// Model selector on welcome screen
	ModelSelectorStyle = lipgloss.NewStyle().
				Foreground(AccentColor).
				Background(SelectionBg).
				Bold(true).
				Padding(0, 1)

	ModelSelectorLabel = lipgloss.NewStyle().
				Foreground(CommentColor)
)

func SidebarLabelValue(label, value string) string {
	labelPart := SidebarLabel.Render(fmt.Sprintf("%-18s", label))
	valuePart := SidebarValue.Render(value)
	return labelPart + valuePart
}
