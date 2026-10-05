package tui

import "charm.land/lipgloss/v2"

// The theme uses the 16 ANSI colours, so it follows the user's terminal palette.
var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	userBarStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	userTextStyle = lipgloss.NewStyle().Bold(true)

	headingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	codeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	italicStyle  = lipgloss.NewStyle().Italic(true)
	strikeStyle  = lipgloss.NewStyle().Strikethrough(true)
	linkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Underline(true)
)
