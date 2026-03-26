package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewToolCalls(width int) string {
	var b strings.Builder

	if len(m.sessions) > 0 {
		s := m.sessions[m.selectedSession]
		b.WriteString(sessionHeader(s) + "\n\n")
	}

	filtered := m.filteredToolCalls()
	if len(filtered) == 0 {
		if len(m.toolCalls) == 0 {
			return b.String() + mutedStyle.Render("  No tool calls recorded")
		}
		return b.String() + mutedStyle.Render(fmt.Sprintf("  No tool calls match %q", m.filterText))
	}

	hdr := fmt.Sprintf("  %-8s %-12s %-30s %8s  %s", "TIME", "TOOL", "TARGET", "DURATION", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		tc := filtered[i]
		timeStr := tc.StartTime.Format("15:04:05")
		dur := fmt.Sprintf("%.1fs", float64(tc.DurationMs)/1000)
		if tc.DurationMs == 0 {
			dur = "..."
		}

		status := statusOk.String()
		switch tc.Status {
		case "fail":
			status = statusFail.String()
		case "pending":
			status = statusActive.String()
		case "interrupted":
			status = mutedStyle.Render("✗")
		case "retry":
			status = statusRetry.String()
		}

		target := displayTruncate(strings.ReplaceAll(tc.ParamsSummary, "\n", " "), 30)
		toolName := tc.ToolName
		if len(toolName) > 12 {
			toolName = toolName[:12]
		}

		line := fmt.Sprintf("  %s %-12s %-30s %8s  %s",
			mutedStyle.Render(timeStr),
			headerStyle.Render(toolName),
			target, dur, status)

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")

		if !m.expandedCalls[tc.CallID] {
			continue
		}
		if tc.ParamsSummary != "" {
			b.WriteString(fmt.Sprintf("    %s %s\n",
				keyStyle.Render("Params:"),
				mutedStyle.Render(tc.ParamsSummary)))
		}
		if tc.ResultSummary != "" {
			b.WriteString(fmt.Sprintf("    %s %s\n",
				keyStyle.Render("Result:"),
				mutedStyle.Render(tc.ResultSummary)))
		}
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more", len(filtered)-end)) + "\n")
	}

	return b.String()
}
