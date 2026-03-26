package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewTimeline(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(sessionHeader(s) + "\n\n")

	filtered := m.filteredTimeline()
	if len(filtered) == 0 {
		if len(m.timelineEntries) == 0 {
			return b.String() + mutedStyle.Render("  No events recorded")
		}
		return b.String() + mutedStyle.Render(fmt.Sprintf("  No events match %q", m.filterText))
	}

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		e := filtered[i]
		timeStr := e.time.Format("15:04:05")
		icon := "── "
		switch e.status {
		case "fail":
			icon = statusFail.String() + " "
		case "success", "ok", "end":
			icon = statusOk.String() + " "
		case "start":
			icon = statusActive.String() + " "
		}

		detail := displayTruncate(e.detail, width-16)
		line := fmt.Sprintf("  %s %s %s", mutedStyle.Render(timeStr), icon, detail)
		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more", len(filtered)-end)) + "\n")
	}

	return b.String()
}
