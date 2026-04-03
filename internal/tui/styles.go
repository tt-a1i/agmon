package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary     = lipgloss.Color("#7C3AED") // purple
	colorSecondary   = lipgloss.Color("#06B6D4") // cyan
	colorSuccess     = lipgloss.Color("#22C55E") // green
	colorError       = lipgloss.Color("#EF4444") // red
	colorWarning     = lipgloss.Color("#F59E0B") // amber
	colorMuted       = lipgloss.Color("#6B7280") // gray
	colorBorder      = lipgloss.Color("#3F3F5F") // border
	colorWhite       = lipgloss.Color("#E5E7EB") // white-ish for normal text
	colorHighlight   = lipgloss.Color("#A78BFA") // lighter purple for highlights
	colorInfo        = lipgloss.Color("#93C5FD") // soft blue for dashboard values
	colorClaudeBadge = lipgloss.Color("#94A3B8") // slate blue for Claude badge

	// Title
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			PaddingLeft(1)

	// Tabs
	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colorPrimary).
			Padding(0, 2)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 2)

	// Content box
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// Column headers
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	dashboardMetricStyle = lipgloss.NewStyle().
				Foreground(colorInfo).
				Bold(true)

	claudeBadgeStyle = lipgloss.NewStyle().
				Width(dashboardBadgeWidth).
				Foreground(colorClaudeBadge).
				Bold(true)

	codexBadgeStyle = lipgloss.NewStyle().
			Width(dashboardBadgeWidth).
			Foreground(colorSuccess).
			Bold(true)

	// Status indicators
	statusActive = lipgloss.NewStyle().
			Foreground(colorSuccess).
			SetString("●")

	statusFail = lipgloss.NewStyle().
			Foreground(colorError).
			SetString("✗")

	statusOk = lipgloss.NewStyle().
			Foreground(colorSuccess).
			SetString("✓")

	statusRetry = lipgloss.NewStyle().
			Foreground(colorWarning).
			SetString("↻")

	// Text styles
	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3D3D6F")).
			Foreground(colorWhite).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	costStyle    = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	bigCostStyle = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)

	// Context window usage
	contextOkStyle     = lipgloss.NewStyle().Foreground(colorSuccess)
	contextWarnStyle   = lipgloss.NewStyle().Foreground(colorWarning)
	contextDangerStyle = lipgloss.NewStyle().Foreground(colorError).Bold(true)

	// Filter mode
	filterLabelStyle = lipgloss.NewStyle().
				Foreground(colorWarning).
				Bold(true)

	filterInputStyle = lipgloss.NewStyle().
				Foreground(colorWhite).
				Bold(true)

	// Footer keybinding styles
	keyStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	keyDescStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Flash message
	flashStyle = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	// Messages tab
	msgPromptStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	msgTextStyle = lipgloss.NewStyle().
			Foreground(colorWhite)

	// Session tag
	tagStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Italic(true)
)
