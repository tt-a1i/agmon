package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tt-a1i/agmon/internal/storage"
)

func (m *Model) refreshFilteredViews() {
	filter := strings.ToLower(m.filterText)
	m.filteredSessionsCache = filterSessions(m.sessions, filter, m.platformFilter, m.dashboardSort)
	m.filteredToolCallsCache = filterToolCalls(m.toolCalls, filter)
}

func (m *Model) setFilterText(text string) {
	m.filterText = text
	m.refreshFilteredViews()
}

func (m *Model) resetFilter() {
	m.filterMode = false
	m.setFilterText("")
}

func (m *Model) resetListPosition() {
	m.selectedRow = 0
	m.viewOffset = 0
}

func (m *Model) clearToolExpanded() {
	for k := range m.expandedCalls {
		if !strings.HasPrefix(k, "msg-") {
			delete(m.expandedCalls, k)
		}
	}
}

func (m *Model) pruneExpandedCalls() {
	if len(m.expandedCalls) == 0 {
		return
	}

	valid := make(map[string]struct{}, len(m.messages)+len(m.toolCalls))
	for i := range m.messages {
		valid[fmt.Sprintf("msg-%d", i)] = struct{}{}
	}
	for _, tc := range m.toolCalls {
		valid[tc.CallID] = struct{}{}
	}

	for key := range m.expandedCalls {
		if _, ok := valid[key]; !ok {
			delete(m.expandedCalls, key)
		}
	}
}

func filterSessions(sessions []storage.SessionRow, filter string, platform sessionPlatformFilter, order dashboardSort) []storage.SessionRow {
	out := make([]storage.SessionRow, 0, len(sessions))
	for _, s := range sessions {
		if !matchesPlatformFilter(s, platform) {
			continue
		}
		if filter == "" ||
			strings.Contains(strings.ToLower(sessionDisplayName(s)), filter) ||
			strings.Contains(strings.ToLower(s.Platform), filter) {
			out = append(out, s)
		}
	}
	sortSessions(out, order)
	return out
}

func matchesPlatformFilter(s storage.SessionRow, platform sessionPlatformFilter) bool {
	switch platform {
	case platformClaude:
		return s.Platform == "claude"
	case platformCodex:
		return s.Platform == "codex"
	default:
		return true
	}
}

func sortSessions(sessions []storage.SessionRow, order dashboardSort) {
	switch order {
	case sortCost:
		sort.SliceStable(sessions, func(i, j int) bool {
			return sessions[i].TotalCostUSD > sessions[j].TotalCostUSD
		})
	default:
		// Keep DB order: newest first.
	}
}

func filterToolCalls(toolCalls []storage.ToolCallRow, filter string) []storage.ToolCallRow {
	if filter == "" {
		return toolCalls
	}
	out := make([]storage.ToolCallRow, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if strings.Contains(strings.ToLower(tc.ToolName), filter) ||
			strings.Contains(strings.ToLower(tc.ParamsSummary), filter) {
			out = append(out, tc)
		}
	}
	return out
}
