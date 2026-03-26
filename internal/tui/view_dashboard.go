package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
		} else {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions match %q", m.filterText)))
		}
		return b.String()
	}

	hdr := fmt.Sprintf("  %-22s %-14s  %8s  %7s  %s", "SESSION", "STARTED", "COST", "CTX", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		s := filtered[i]
		status := lipgloss.NewStyle().Foreground(colorSuccess).Render("● run")
		switch s.Status {
		case "ended":
			status = mutedStyle.Render("  end")
		case "stale":
			status = mutedStyle.Render("  ---")
		}

		badge := platformBadge(s.Platform)
		name := displayTruncate(sessionDisplayName(s), 18)
		started := formatStartTime(s.StartTime)

		costText := fmt.Sprintf("$%.2f", s.TotalCostUSD)
		if s.TotalCostUSD < 0.005 {
			costText = "-"
		}
		costPad := fmt.Sprintf("%8s", costText)
		ctxText := formatTokens(s.LatestContextTokens)
		if s.LatestContextTokens == 0 {
			ctxText = "-"
		}
		ctxPad := fmt.Sprintf("%7s", ctxText)

		line := fmt.Sprintf("  %s %-18s %s  %s  %s  %s",
			badge, name,
			mutedStyle.Render(fmt.Sprintf("%-14s", started)),
			costStyle.Render(costPad),
			contextColorize(s.LatestContextTokens, s.Model, ctxPad),
			status)

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
		preview := fmt.Sprintf(" %s %s    %s %s    %s %s    %s %s    %s %s",
			mutedStyle.Render("▸"), headerStyle.Render(sessionDisplayName(s)),
			mutedStyle.Render("In"), headerStyle.Render(formatTokens(s.TotalInputTokens)),
			mutedStyle.Render("Out"), headerStyle.Render(formatTokens(s.TotalOutputTokens)),
			mutedStyle.Render("Ctx"), contextPercent(s.LatestContextTokens, s.Model),
			mutedStyle.Render("Cost"), costStyle.Render(fmt.Sprintf("$%.2f", s.TotalCostUSD)))
		if cr := cacheHitRate(s); cr != "" {
			preview += "    " + mutedStyle.Render(cr)
		}
		b.WriteString(preview)
	}

	return b.String()
}
