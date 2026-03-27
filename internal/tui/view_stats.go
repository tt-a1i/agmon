package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/tt-a1i/agmon/internal/storage"
)

// buildStatsLines builds all display lines for the Stats tab.
// Used by both viewStats (rendering) and refresh (line count for scrolling).
func (m Model) buildStatsLines(width int) []string {
	if len(m.sessions) == 0 {
		return []string{mutedStyle.Render("  No sessions")}
	}

	var lines []string

	s := m.sessions[m.selectedSession]
	lines = append(lines, sessionHeader(s))
	lines = append(lines, "")

	// --- Session overview ---
	dur := "-"
	if s.EndTime != nil {
		dur = s.EndTime.Sub(s.StartTime).Round(time.Second).String()
	} else if s.Status == "active" {
		dur = time.Since(s.StartTime).Round(time.Second).String() + " (active)"
	}
	model := s.Model
	if model == "" {
		model = "-"
	}
	lines = append(lines, fmt.Sprintf("  %s %s    %s %s    %s %d    %s %d",
		mutedStyle.Render("Duration:"), headerStyle.Render(dur),
		mutedStyle.Render("Model:"), headerStyle.Render(model),
		mutedStyle.Render("Agents:"), len(m.agents),
		mutedStyle.Render("Files:"), len(m.fileChanges)))

	// --- Cache breakdown ---
	cacheCreate := s.TotalCacheCreationTokens
	cacheRead := s.TotalCacheReadTokens
	if cacheCreate > 0 || cacheRead > 0 {
		total := cacheCreate + cacheRead
		hitPct := float64(cacheRead) / float64(total) * 100
		lines = append(lines, fmt.Sprintf("  %s %s    %s %s    %s %.0f%%",
			mutedStyle.Render("Cache Create:"), headerStyle.Render(formatTokens(cacheCreate)),
			mutedStyle.Render("Cache Read:"), headerStyle.Render(formatTokens(cacheRead)),
			mutedStyle.Render("Hit Rate:"), hitPct))
	}
	lines = append(lines, "")

	// --- Tool usage ---
	if len(m.toolStats) > 0 {
		lines = append(lines, headerStyle.Render("  Tool Usage"))
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("  %-16s %6s %8s %6s", "TOOL", "COUNT", "AVG", "FAIL")))

		limit := len(m.toolStats)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			ts := m.toolStats[i]
			avgStr := "-"
			if ts.AvgMs > 0 {
				avgStr = fmt.Sprintf("%.1fs", float64(ts.AvgMs)/1000)
			}
			failStr := "-"
			if ts.FailCount > 0 {
				failStr = errorStyle.Render(fmt.Sprintf("%d", ts.FailCount))
			}
			lines = append(lines, fmt.Sprintf("  %-16s %6d %8s %6s",
				displayTruncate(ts.ToolName, 16), ts.Count, avgStr, failStr))
		}
		if len(m.toolStats) > limit {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ... %d more tools", len(m.toolStats)-limit)))
		}
		lines = append(lines, "")
	}

	// --- Agent breakdown (aggregated by role) ---
	if len(m.agentStats) > 0 {
		groups := aggregateAgentsByRole(m.agentStats)

		lines = append(lines, headerStyle.Render("  Agents"))
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("  %-20s %6s %6s %8s", "ROLE", "COUNT", "DONE", "TOOLS")))

		for _, g := range groups {
			statusStr := fmt.Sprintf("%d/%d", g.ended, g.count)
			if g.ended == g.count {
				statusStr = mutedStyle.Render(statusStr)
			} else {
				statusStr = headerStyle.Render(statusStr)
			}
			toolStr := "-"
			if g.toolCalls > 0 {
				toolStr = fmt.Sprintf("%d", g.toolCalls)
			}
			prefix := "  "
			if g.isChild {
				prefix = "  └ "
			}
			lines = append(lines, fmt.Sprintf("%s%-18s %6d %6s %8s",
				prefix, displayTruncate(g.role, 18), g.count, statusStr, toolStr))
		}
		lines = append(lines, "")
	}

	// --- File changes summary ---
	if len(m.fileChanges) > 0 {
		creates, edits, deletes := 0, 0, 0
		for _, fc := range m.fileChanges {
			switch fc.ChangeType {
			case "create":
				creates++
			case "delete":
				deletes++
			default:
				edits++
			}
		}
		parts := []string{headerStyle.Render("  File Changes")}
		if creates > 0 {
			parts = append(parts, fmt.Sprintf("+%d", creates))
		}
		if edits > 0 {
			parts = append(parts, fmt.Sprintf("~%d", edits))
		}
		if deletes > 0 {
			parts = append(parts, fmt.Sprintf("-%d", deletes))
		}
		lines = append(lines, strings.Join(parts, " "))

		limit := len(m.fileChanges)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			fc := m.fileChanges[i]
			icon := "~"
			switch fc.ChangeType {
			case "create":
				icon = "+"
			case "delete":
				icon = "-"
			}
			lines = append(lines, fmt.Sprintf("  %s %s", icon, displayTruncate(fc.FilePath, width-6)))
		}
		if len(m.fileChanges) > limit {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ... %d more files", len(m.fileChanges)-limit)))
		}
	}

	return lines
}

func (m Model) viewStats(width int) string {
	lines := m.buildStatsLines(width)

	// Apply viewport scrolling
	visible := m.tabVisibleRows()
	start := m.viewOffset
	if start >= len(lines) {
		start = 0
	}
	end := start + visible
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for _, line := range lines[start:end] {
		b.WriteString(line + "\n")
	}
	if end < len(lines) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more lines (j to scroll)", len(lines)-end)))
	}

	return b.String()
}

type agentRoleGroup struct {
	role      string
	isChild   bool
	count     int
	ended     int
	toolCalls int
}

func aggregateAgentsByRole(agents []storage.AgentStatRow) []agentRoleGroup {
	type key struct {
		role    string
		isChild bool
	}
	m := make(map[key]*agentRoleGroup)
	var order []key

	for _, a := range agents {
		role := a.Role
		if role == "" {
			role = "main"
		}
		k := key{role: role, isChild: a.ParentAgentID != ""}
		g, ok := m[k]
		if !ok {
			g = &agentRoleGroup{role: role, isChild: k.isChild}
			m[k] = g
			order = append(order, k)
		}
		g.count++
		if a.Status == "ended" {
			g.ended++
		}
		g.toolCalls += a.ToolCallCount
	}

	result := make([]agentRoleGroup, 0, len(order))
	for _, k := range order {
		result = append(result, *m[k])
	}
	return result
}
