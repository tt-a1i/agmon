package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.splash {
		return m.viewSplash()
	}

	var b strings.Builder

	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render(name))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(name))
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n")

	contentWidth := m.width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	var content string
	switch m.activeTab {
	case tabDashboard:
		content = m.viewDashboard(contentWidth)
	case tabMessages:
		content = m.viewMessages(contentWidth)
	case tabToolCalls:
		content = m.viewToolCalls(contentWidth)
	case tabTimeline:
		content = m.viewTimeline(contentWidth)
	}

	b.WriteString(boxStyle.Width(contentWidth).Render(content))
	b.WriteString("\n")
	b.WriteString(m.footer())

	return b.String()
}

func (m Model) footer() string {
	var footer string
	switch {
	case m.flashMsg != "" && time.Now().Before(m.flashExpire):
		footer = " " + flashStyle.Render("✓ "+m.flashMsg)
	case m.filterMode:
		footer = fmt.Sprintf(" %s %s%s  %s  %s",
			filterLabelStyle.Render("Filter:"),
			filterInputStyle.Render(m.filterText),
			filterLabelStyle.Render("█"),
			fmtKey("Esc", "cancel"),
			fmtKey("Enter", "confirm"))
	case m.filterText != "":
		footer = fmt.Sprintf(" %s %s  %s  %s  %s",
			filterLabelStyle.Render("Filter:"),
			filterInputStyle.Render(m.filterText),
			fmtKey("Esc", "clear"),
			fmtKey("Tab", "view"),
			fmtKey("q", "quit"))
	case m.activeTab == tabDashboard:
		footer = fmt.Sprintf(" %s  %s  %s  %s  %s  %s  %s  %s  %s",
			fmtKey("t", "range"),
			fmtKey("p", "plat:"+platformFilterNames[m.platformFilter]),
			fmtKey("s", "sort:"+dashboardSortNames[m.dashboardSort]),
			fmtKey("/", "filter"),
			fmtKey("Tab", "view"),
			fmtKey("j/k", "nav"),
			fmtKey("c", "copy"),
			fmtKey("Enter", "msgs"),
			fmtKey("q", "quit"))
	default:
		pos := ""
		if len(m.sessions) > 1 {
			pos = fmt.Sprintf("  %s",
				mutedStyle.Render(fmt.Sprintf("session %d/%d", m.selectedSession+1, len(m.sessions))))
		}
		enterHint := ""
		if m.activeTab == tabMessages || m.activeTab == tabToolCalls {
			enterHint = fmt.Sprintf("  %s", fmtKey("Enter", "expand"))
		}
		filterHint := ""
		if m.activeTab != tabMessages {
			filterHint = fmt.Sprintf("  %s", fmtKey("/", "filter"))
		}
		footer = fmt.Sprintf(" %s  %s  %s  %s%s%s  %s%s",
			fmtKey("Tab", "view"),
			fmtKey("[/]", "session"),
			fmtKey("j/k", "nav"),
			fmtKey("c", "copy"),
			enterHint,
			filterHint,
			fmtKey("q", "quit"),
			pos)
	}

	if m.err != nil {
		footer += "  " + errorStyle.Render("err: "+m.err.Error())
	}
	if m.updateAvailable != "" {
		footer += "  " + mutedStyle.Render("v"+m.updateAvailable+" available — ") + keyStyle.Render("agmon update")
	}
	return footer
}
