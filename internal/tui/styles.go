package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSecondary = lipgloss.Color("#06B6D4") // cyan
	colorSuccess   = lipgloss.Color("#22C55E") // green
	colorError     = lipgloss.Color("#EF4444") // red
	colorWarning   = lipgloss.Color("#F59E0B") // amber
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorBg        = lipgloss.Color("#1E1E2E") // dark bg
	colorBorder    = lipgloss.Color("#3F3F5F") // border

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			PaddingLeft(1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colorPrimary).
			Padding(0, 2)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 2)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	statusActive = lipgloss.NewStyle().
			Foreground(colorSuccess).
			SetString("●")

	statusEnded = lipgloss.NewStyle().
			Foreground(colorMuted).
			SetString("◌")

	statusFail = lipgloss.NewStyle().
			Foreground(colorError).
			SetString("✗")

	statusOk = lipgloss.NewStyle().
			Foreground(colorSuccess).
			SetString("✓")

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#2D2D4F")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	costStyle = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)

	contextOkStyle     = lipgloss.NewStyle().Foreground(colorSuccess)  // <50%
	contextWarnStyle   = lipgloss.NewStyle().Foreground(colorWarning)   // 50-80%
	contextDangerStyle = lipgloss.NewStyle().Foreground(colorError)     // >80%
)
