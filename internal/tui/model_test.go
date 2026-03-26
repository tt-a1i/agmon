package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

func testModelDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedModelSession(t *testing.T, db *storage.DB) {
	t.Helper()
	now := time.Now().UTC()

	if err := db.UpsertSession("session-1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpdateSessionMeta("session-1", "/tmp/agmon", "main"); err != nil {
		t.Fatalf("update session meta: %v", err)
	}
	if err := db.UpsertAgent("agent-1", "session-1", "", "main", now); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if err := db.InsertToolCallStart("call-1", "agent-1", "session-1", "Edit", "{}", now.Add(time.Second)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if err := db.UpdateToolCallEnd("call-1", "ok", event.StatusSuccess, 250, now.Add(2*time.Second)); err != nil {
		t.Fatalf("update tool call: %v", err)
	}
	if err := db.InsertFileChange("session-1", "foo.go", event.FileEdit, now.Add(3*time.Second)); err != nil {
		t.Fatalf("insert file change: %v", err)
	}
	if err := db.InsertTokenUsage("agent-1", "session-1", 500, 120, 0, 0, "sonnet", 0.42, now, "src-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	if err := db.UpdateSessionTokens("session-1"); err != nil {
		t.Fatalf("update session tokens: %v", err)
	}
}

func TestModelRefreshLoadsSelectedSessionData(t *testing.T) {
	db := testModelDB(t)
	seedModelSession(t, db)

	m := NewModel(db, make(chan EventMsg, 1))
	m.refresh()

	if len(m.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(m.sessions))
	}
	if len(m.agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(m.agents))
	}
	if len(m.toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(m.toolCalls))
	}
	if len(m.fileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(m.fileChanges))
	}
	if len(m.timelineEntries) != 3 {
		t.Fatalf("expected 3 timeline entries, got %d", len(m.timelineEntries))
	}
	if m.todayInput != 500 || m.todayOutput != 120 {
		t.Fatalf("unexpected today totals: in=%d out=%d", m.todayInput, m.todayOutput)
	}
}

func TestModelUpdateEnterOnDashboardSwitchesToMessages(t *testing.T) {
	db := testModelDB(t)
	seedModelSession(t, db)

	m := NewModel(db, make(chan EventMsg, 1))
	m.refresh()
	m.activeTab = tabDashboard
	m.selectedRow = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if next.activeTab != tabMessages {
		t.Fatalf("expected messages tab, got %v", next.activeTab)
	}
	if next.selectedRow != 0 || next.viewOffset != 0 {
		t.Fatalf("expected messages view to reset cursor, got row=%d offset=%d", next.selectedRow, next.viewOffset)
	}
}

func TestModelUpdateFilterModeCapturesRunesAndEscClears(t *testing.T) {
	db := testModelDB(t)
	seedModelSession(t, db)

	m := NewModel(db, make(chan EventMsg, 1))
	m.refresh()
	m.activeTab = tabDashboard

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	filtering := updated.(Model)
	if !filtering.filterMode {
		t.Fatal("expected filter mode to be enabled")
	}

	updated, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 'g'}})
	filtering = updated.(Model)
	if filtering.filterText != "ag" {
		t.Fatalf("expected filter text to capture runes, got %q", filtering.filterText)
	}

	updated, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyEsc})
	cleared := updated.(Model)
	if cleared.filterMode {
		t.Fatal("expected esc to exit filter mode")
	}
	if cleared.filterText != "" {
		t.Fatalf("expected esc to clear filter text, got %q", cleared.filterText)
	}
}

func TestBuildTimelineOrdersEntriesChronologically(t *testing.T) {
	start := time.Now()
	end := start.Add(2 * time.Second)
	toolStart := start.Add(time.Second)
	fileTime := end.Add(time.Second)

	entries := buildTimeline(
		[]storage.AgentRow{{
			AgentID:   "agent-1",
			Role:      "main",
			StartTime: start,
			EndTime:   &end,
		}},
		[]storage.ToolCallRow{{
			CallID:     "call-1",
			ToolName:   "Edit",
			StartTime:  toolStart,
			Status:     "success",
			DurationMs: 250,
		}},
		[]storage.FileChangeRow{{
			FilePath:   "foo.go",
			ChangeType: "edit",
			Timestamp:  fileTime,
		}},
	)

	if len(entries) != 4 {
		t.Fatalf("expected 4 timeline entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].time.Before(entries[i-1].time) {
			t.Fatalf("entries not ordered: %+v", entries)
		}
	}
}

func TestClearMsgExpandedOnlyRemovesMessageKeys(t *testing.T) {
	m := Model{
		expandedCalls: map[string]bool{
			"msg-0":  true,
			"msg-1":  true,
			"call-1": true,
		},
	}

	m.clearMsgExpanded()

	if len(m.expandedCalls) != 1 {
		t.Fatalf("expected only non-message expansion to remain, got %#v", m.expandedCalls)
	}
	if !m.expandedCalls["call-1"] {
		t.Fatalf("expected tool expansion to remain, got %#v", m.expandedCalls)
	}
}

func TestRefreshFilteredViewsCachesCurrentFilterResults(t *testing.T) {
	m := Model{
		sessions: []storage.SessionRow{
			{SessionID: "alpha", Platform: "claude", CWD: "/tmp/alpha"},
			{SessionID: "beta", Platform: "codex", CWD: "/tmp/beta"},
		},
		toolCalls: []storage.ToolCallRow{
			{CallID: "call-1", ToolName: "Edit", ParamsSummary: "alpha.txt"},
			{CallID: "call-2", ToolName: "Bash", ParamsSummary: "ls beta"},
		},
		timelineEntries: []timelineEntry{
			{kind: "tool", detail: "Edit alpha.txt"},
			{kind: "file", detail: "edit beta.txt"},
		},
	}

	m.setFilterText("beta")

	if got := len(m.filteredSessions()); got != 1 || m.filteredSessions()[0].SessionID != "beta" {
		t.Fatalf("unexpected filtered sessions: %#v", m.filteredSessions())
	}
	if got := len(m.filteredToolCalls()); got != 1 || m.filteredToolCalls()[0].CallID != "call-2" {
		t.Fatalf("unexpected filtered tool calls: %#v", m.filteredToolCalls())
	}
	if got := len(m.filteredTimeline()); got != 1 || m.filteredTimeline()[0].detail != "edit beta.txt" {
		t.Fatalf("unexpected filtered timeline: %#v", m.filteredTimeline())
	}

	m.setFilterText("")
	if got := len(m.filteredSessions()); got != 2 {
		t.Fatalf("expected all sessions after clearing filter, got %d", got)
	}
	if got := len(m.filteredToolCalls()); got != 2 {
		t.Fatalf("expected all tool calls after clearing filter, got %d", got)
	}
	if got := len(m.filteredTimeline()); got != 2 {
		t.Fatalf("expected all timeline entries after clearing filter, got %d", got)
	}
}

func TestPruneExpandedCallsDropsStaleEntries(t *testing.T) {
	m := Model{
		messages: []collector.UserMessage{
			{Content: "first"},
			{Content: "second"},
		},
		toolCalls: []storage.ToolCallRow{
			{CallID: "call-1"},
		},
		expandedCalls: map[string]bool{
			"msg-0":  true,
			"msg-9":  true,
			"call-1": true,
			"call-9": true,
		},
	}

	m.pruneExpandedCalls()

	if len(m.expandedCalls) != 2 {
		t.Fatalf("expected only current session expansions to remain, got %#v", m.expandedCalls)
	}
	if !m.expandedCalls["msg-0"] || !m.expandedCalls["call-1"] {
		t.Fatalf("expected current expansions to remain, got %#v", m.expandedCalls)
	}
}
