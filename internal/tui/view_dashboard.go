package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m Model) viewDashboard(width int) string {
	var b strings.Builder

	rangeName := rangeNames[m.summaryRange]

	// Big cost with trend
	costStr := fmt.Sprintf("$%.2f", m.todayCost)
	trend := renderTrend(m.todayCost, m.prevCost)

	b.WriteString(fmt.Sprintf(" %s  %s %s    %s %s %s %s\n\n",
		mutedStyle.Render(rangeName),
		bigCostStyle.Render(costStr), trend,
		mutedStyle.Render("In"), headerStyle.Render(formatTokens(m.todayInput)),
		mutedStyle.Render("Out"), headerStyle.Render(formatTokens(m.todayOutput))))

	if projection := m.renderProjectionSummary(); projection != "" {
		b.WriteString(projection + "\n\n")
	}

	if workspaceLine := m.renderWorkspaceScope(); workspaceLine != "" {
		b.WriteString(workspaceLine + "\n\n")
	}

	budgetLine := m.renderBudgetChips()
	tagLine := m.renderTagChips()
	if budgetLine != "" {
		b.WriteString(budgetLine + "\n")
	}
	if tagLine != "" {
		b.WriteString(tagLine + "\n")
	}
	if budgetLine != "" || tagLine != "" {
		b.WriteString("\n")
	}

	filtered := m.filteredSessions()
	if len(filtered) == 0 {
		if len(m.sessions) == 0 {
			msg := "  No sessions yet."
			if !claudeHooksConfigured() {
				msg += "\n\n  Run 'tm setup' to configure Claude Code hooks,\n  then start using Claude Code normally."
			} else {
				msg += "\n  Start using Claude Code or Codex to see data."
			}
			b.WriteString(mutedStyle.Render(msg))
		} else if m.filterText != "" {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions match %q", m.filterText)))
		} else if m.tagFilter != tagFilterAll {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions tagged %q", tagFilterDisplayName(m.tagFilter))))
		} else if m.workspaceFilter && m.workspace != "" {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions in workspace %s", displayWorkspacePath(m.workspace))))
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
		isAnomaly := m.costAnomalies[s.SessionID]
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

		prefix := "  "
		if isAnomaly {
			prefix = contextWarnStyle.Render("⚡ ")
		}
		line := fmt.Sprintf("%s%s %-16s %s  %s  %s  %s",
			prefix, badge, name,
			mutedStyle.Render(fmt.Sprintf("%-14s", started)),
			costStyle.Render(costPad),
			dashboardMetricStyle.Render(inPad),
			dashboardMetricStyle.Render(outPad))
		line += tagText

		if isAnomaly {
			line = contextWarnStyle.Render(line)
		}
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

func (m Model) renderWorkspaceScope() string {
	if !m.workspaceFilter || m.workspace == "" {
		return ""
	}
	return fmt.Sprintf(" %s %s · %d of %d sessions\n %s",
		mutedStyle.Render("Workspace:"),
		headerStyle.Render(displayWorkspacePath(m.workspace)),
		workspaceSessionCount(m.sessions, m.workspace),
		len(m.sessions),
		mutedStyle.Render("Press W to toggle workspace filter · A to show all"))
}

func displayWorkspacePath(path string) string {
	cleanPath := filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return cleanPath
	}
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "~"
	}
	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return cleanPath
	}
	return filepath.Join("~", rel)
}

func (m Model) renderProjectionSummary() string {
	if m.projection.DaysInMonth == 0 {
		return ""
	}
	arrow := ""
	if m.projection.ProjectedTotal > m.projection.UsedSoFar {
		arrow = mutedStyle.Render("↑")
	}
	confidence := renderProjectionConfidence(m.projection.Confidence)
	return fmt.Sprintf(" %s %s %s      %s %s %s\n %s      %s",
		mutedStyle.Render("This month"),
		costStyle.Render(formatProjectionCost(m.projection.UsedSoFar)),
		arrow,
		mutedStyle.Render("Projected by EOM"),
		costStyle.Render(formatProjectionCost(m.projection.ProjectedTotal)),
		confidence,
		mutedStyle.Render(fmt.Sprintf("%d sessions", len(m.sessions))),
		mutedStyle.Render(fmt.Sprintf("Based on %d days", m.projection.DaysElapsed)))
}

func renderProjectionConfidence(confidence string) string {
	if confidence == "" {
		confidence = "low"
	}
	text := "(" + confidence + ")"
	switch confidence {
	case "high":
		return keyStyle.Render(text)
	case "medium":
		return contextWarnStyle.Render(text)
	default:
		return mutedStyle.Render(text)
	}
}

func formatProjectionCost(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}
