package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestMessageExpansionKeyStableForSameMessage(t *testing.T) {
	ts := time.Date(2026, 1, 14, 12, 0, 0, 0, time.UTC)
	msg1 := collector.UserMessage{Timestamp: ts, Content: "hello"}
	msg2 := collector.UserMessage{Timestamp: ts, Content: "hello"}
	if messageExpansionKey(msg1) != messageExpansionKey(msg2) {
		t.Error("same timestamp+content should produce identical key")
	}
}

func TestMessageExpansionKeyDistinguishesContent(t *testing.T) {
	ts := time.Date(2026, 1, 14, 12, 0, 0, 0, time.UTC)
	a := collector.UserMessage{Timestamp: ts, Content: "hello"}
	b := collector.UserMessage{Timestamp: ts, Content: "world"}
	if messageExpansionKey(a) == messageExpansionKey(b) {
		t.Error("different content should produce different keys")
	}
}

func TestClearToolExpandedPreservesMessageKeys(t *testing.T) {
	m := &Model{
		expandedCalls: map[string]bool{
			"msg-0-...": true,
			"call-edit": true,
			"msg-1-foo": true,
			"call-bash": true,
		},
	}
	m.clearToolExpanded()

	// msg- prefix preserved, others cleared.
	for k, v := range m.expandedCalls {
		if strings.HasPrefix(k, "msg-") {
			if !v {
				t.Errorf("message key %q should be preserved", k)
			}
		} else {
			t.Errorf("non-message key %q should be cleared", k)
		}
	}
	if len(m.expandedCalls) != 2 {
		t.Errorf("expected 2 msg keys remaining, got %d", len(m.expandedCalls))
	}
}

// TestCurrentTabRowCountEachTab covers all four tab branches + the unknown
// fall-through (returns 0). The function is a small pure dispatcher but it
// gates j/k navigation bounds — a regression here would either freeze
// navigation or let cursor escape the visible range.
func TestCurrentTabRowCountEachTab(t *testing.T) {
	m := Model{
		filteredSessionsCache:  make([]storage.SessionRow, 3),
		filteredMessagesCache:  make([]collector.UserMessage, 5),
		filteredToolCallsCache: make([]storage.ToolCallRow, 7),
		statsLineCount:         11,
	}

	cases := []struct {
		tab  tab
		want int
	}{
		{tabDashboard, 3},
		{tabMessages, 5},
		{tabToolCalls, 7},
		{tabStats, 11},
		{tab(99), 0}, // out-of-range falls through
	}
	for _, c := range cases {
		m.activeTab = c.tab
		if got := m.currentTabRowCount(); got != c.want {
			t.Errorf("tab=%d: got %d, want %d", c.tab, got, c.want)
		}
	}
}

func TestDashboardStatusEachState(t *testing.T) {
	for _, c := range []struct {
		status, mustContain string
	}{
		{"ended", "end"},
		{"stale", "---"},
		{"active", "run"},
		{"unknown", "run"}, // default branch
	} {
		got := dashboardStatus(c.status)
		if !strings.Contains(got, c.mustContain) {
			t.Errorf("dashboardStatus(%q) = %q, expected to contain %q", c.status, got, c.mustContain)
		}
	}
}
