package tui

import (
	"fmt"
	"strings"

	"github.com/tt-a1i/agmon/internal/storage"
)

func (m *Model) refreshFilteredViews() {
	filter := strings.ToLower(m.filterText)
	m.filteredSessionsCache = filterSessions(m.sessions, filter)
	m.filteredToolCallsCache = filterToolCalls(m.toolCalls, filter)
	m.filteredTimelineCache = filterTimeline(m.timelineEntries, filter)
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

func filterSessions(sessions []storage.SessionRow, filter string) []storage.SessionRow {
	if filter == "" {
		return sessions
	}
	out := make([]storage.SessionRow, 0, len(sessions))
	for _, s := range sessions {
		if strings.Contains(strings.ToLower(sessionDisplayName(s)), filter) ||
			strings.Contains(strings.ToLower(s.Platform), filter) {
			out = append(out, s)
		}
	}
	return out
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

func filterTimeline(entries []timelineEntry, filter string) []timelineEntry {
	if filter == "" {
		return entries
	}
	out := make([]timelineEntry, 0, len(entries))
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.detail), filter) ||
			strings.Contains(strings.ToLower(e.kind), filter) {
			out = append(out, e)
		}
	}
	return out
}
