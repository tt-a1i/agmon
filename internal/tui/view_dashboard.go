package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewDashboard(width int) string {
	var b strings.Builder

	rangeName := rangeNames[m.summaryRange]
	b.WriteString(fmt.Sprintf(" %s %s %s %s    %s %s\n\n",
		mutedStyle.Render(rangeName+" In"), headerStyle.Render(formatTokens(m.todayInput)),
		mutedStyle.Render("/ Out"), headerStyle.Render(formatTokens(m.todayOutput)),
		mutedStyle.Render("Cost"), costStyle.Render(fmt.Sprintf("$%.2f", m.todayCost))))

	filtered := m.filteredSessions()
	if len(filtered) == 0 {
		if len(m.sessions) == 0 {
			msg := "  No sessions yet."
			if !claudeHooksConfigured() {
				msg += "\n\n  Run 'agmon setup' to configure Claude Code hooks,\n  then start using Claude Code normally."
			} else {
				msg += "\n  Start using Claude Code or Codex to see data."
			}
			b.WriteString(mutedStyle.Render(msg))
		} else if m.filterText != "" {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions match %q", m.filterText)))
		} else if m.platformFilter != platformAll {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No %s sessions", platformFilterNames[m.platformFilter])))
		} else {
			b.WriteString(mutedStyle.Render("  No sessions"))
		}
		return b.String()
	}

	hdr := fmt.Sprintf("  %-*s %-16s %-14s  %-8s  %-8s  %-8s", dashboardBadgeWidth, "", "SESSION", "STARTED", "COST", "IN", "OUT")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		s := filtered[i]
		badge := platformBadge(s.Platform)
		name := displayTruncate(sessionDisplayName(s), 16)
		started := formatStartTime(s.StartTime)

		costText := fmt.Sprintf("$%.2f", s.TotalCostUSD)
		if s.TotalCostUSD < 0.005 {
			costText = "-"
		}
		costPad := fmt.Sprintf("%-8s", costText)
		inText := formatTokens(s.TotalInputTokens)
		if s.TotalInputTokens == 0 {
			inText = "-"
		}
		inPad := fmt.Sprintf("%-8s", inText)
		outText := formatTokens(s.TotalOutputTokens)
		if s.TotalOutputTokens == 0 {
			outText = "-"
		}
		outPad := fmt.Sprintf("%-8s", outText)

		tagText := ""
		if s.Tag != "" {
			tagText = " " + tagStyle.Render(truncateTag(s.Tag))
		}

		line := fmt.Sprintf("  %s %-16s %s  %s  %s  %s",
			badge, name,
			mutedStyle.Render(fmt.Sprintf("%-14s", started)),
			costStyle.Render(costPad),
			dashboardMetricStyle.Render(inPad),
			dashboardMetricStyle.Render(outPad))
		line += tagText

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more (j to scroll)", len(filtered)-end)) + "\n")
	}

	if m.selectedSession < len(m.sessions) {
		s := m.sessions[m.selectedSession]
		b.WriteString("\n")
		preview := fmt.Sprintf(" %s %s    %s %s    %s %s",
			mutedStyle.Render("▸"), headerStyle.Render(sessionDisplayName(s)),
			mutedStyle.Render("Ctx"), contextPercent(s.LatestContextTokens, s.Model),
			mutedStyle.Render("Status"), dashboardStatus(s.Status))
		if s.Tag != "" {
			preview += "    " + tagStyle.Render(truncateTag(s.Tag))
		}
		if s.CWD != "" {
			preview += "\n " + mutedStyle.Render(displayTruncate(s.CWD, width-4))
		}
		b.WriteString(preview)
	}

	return b.String()
}
