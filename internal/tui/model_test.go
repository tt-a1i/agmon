package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
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
	if err := db.UpdateSessionMeta("session-1", "/tmp/tokenmeter", "main"); err != nil {
		t.Fatalf("update session meta: %v", err)
	}
	if err := db.UpsertAgent("agent-1", "session-1", "", "main", now); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-1", "agent-1", "session-1", "Edit", "{}", now.Add(time.Second)); err != nil {
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
	if len(m.toolStats) < 1 {
		t.Fatalf("expected tool stats, got %d", len(m.toolStats))
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
	}

	m.setFilterText("beta")

	if got := len(m.filteredSessions()); got != 1 || m.filteredSessions()[0].SessionID != "beta" {
		t.Fatalf("unexpected filtered sessions: %#v", m.filteredSessions())
	}
	if got := len(m.filteredToolCalls()); got != 1 || m.filteredToolCalls()[0].CallID != "call-2" {
		t.Fatalf("unexpected filtered tool calls: %#v", m.filteredToolCalls())
	}

	m.setFilterText("")
	if got := len(m.filteredSessions()); got != 2 {
		t.Fatalf("expected all sessions after clearing filter, got %d", got)
	}
	if got := len(m.filteredToolCalls()); got != 2 {
		t.Fatalf("expected all tool calls after clearing filter, got %d", got)
	}
}

func TestPruneExpandedCallsDropsStaleEntries(t *testing.T) {
	msgTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := Model{
		messages: []collector.UserMessage{
			{Timestamp: msgTime, Content: "first"},
			{Timestamp: msgTime.Add(time.Minute), Content: "second"},
		},
		toolCalls: []storage.ToolCallRow{
			{CallID: "call-1"},
		},
		expandedCalls: map[string]bool{
			messageExpansionKeyAt(0, collector.UserMessage{Timestamp: msgTime, Content: "first"}): true,
			"msg-stale": true,
			"call-1":    true,
			"call-9":    true,
		},
	}

	m.pruneExpandedCalls()

	if len(m.expandedCalls) != 2 {
		t.Fatalf("expected only current session expansions to remain, got %#v", m.expandedCalls)
	}
	if !m.expandedCalls[messageExpansionKeyAt(0, m.messages[0])] || !m.expandedCalls["call-1"] {
		t.Fatalf("expected current expansions to remain, got %#v", m.expandedCalls)
	}
}

func TestMessageExpansionUsesStableKeyAfterFiltering(t *testing.T) {
	msgs := []collector.UserMessage{
		{Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Content: "first message"},
		{Timestamp: time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC), Content: "target message"},
	}
	m := Model{
		activeTab:     tabMessages,
		messages:      msgs,
		expandedCalls: make(map[string]bool),
	}
	m.setFilterText("target")
	m.selectedRow = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if next.expandedCalls[messageExpansionKeyAt(0, msgs[0])] {
		t.Fatalf("filtered row 0 should not expand the unfiltered first message: %#v", next.expandedCalls)
	}
	if !next.expandedCalls[messageExpansionKeyAt(1, msgs[1])] {
		t.Fatalf("expected filtered target message to be expanded, got %#v", next.expandedCalls)
	}
}

func TestMessageExpansionDistinguishesDuplicateMessages(t *testing.T) {
	msgTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	msgs := []collector.UserMessage{
		{Timestamp: msgTime, Content: "duplicate"},
		{Timestamp: msgTime, Content: "duplicate"},
	}
	m := Model{
		activeTab:     tabMessages,
		messages:      msgs,
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	m.selectedRow = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if next.expandedCalls[messageExpansionKeyAt(0, msgs[0])] {
		t.Fatalf("first duplicate should not be expanded: %#v", next.expandedCalls)
	}
	if !next.expandedCalls[messageExpansionKeyAt(1, msgs[1])] {
		t.Fatalf("second duplicate should be expanded: %#v", next.expandedCalls)
	}
}

func TestResumeCommandForSessionUsesPlatform(t *testing.T) {
	tests := []struct {
		name string
		row  storage.SessionRow
		want string
	}{
		{
			name: "claude",
			row:  storage.SessionRow{SessionID: "claude-session", Platform: "claude"},
			want: "claude --resume claude-session",
		},
		{
			name: "codex",
			row:  storage.SessionRow{SessionID: "codex-session", Platform: "codex"},
			want: "codex resume codex-session",
		},
		{
			name: "unknown defaults to existing claude behavior",
			row:  storage.SessionRow{SessionID: "unknown-session", Platform: ""},
			want: "claude --resume unknown-session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumeCommandForSession(tt.row); got != tt.want {
				t.Fatalf("resumeCommandForSession() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTabVisibleRowsNeverNegative(t *testing.T) {
	for _, active := range []tab{tabDashboard, tabMessages, tabToolCalls, tabStats} {
		m := Model{activeTab: active, height: 1}
		if got := m.tabVisibleRows(); got < 1 {
			t.Fatalf("tab %v visible rows should be at least 1, got %d", active, got)
		}
	}
}

func TestViewDashboardMovesCtxStatusToPreviewAndShowsPath(t *testing.T) {
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:                "session-1",
				CWD:                      "/Users/admin/code/tokenmeter",
				GitBranch:                "main",
				TotalInputTokens:         527400,
				TotalOutputTokens:        10400,
				LatestContextTokens:      33200,
				TotalCostUSD:             2.86,
				TotalCacheReadTokens:     860,
				TotalCacheCreationTokens: 140,
				Model:                    "sonnet",
			},
		},
		filteredSessionsCache: []storage.SessionRow{
			{
				SessionID:           "session-1",
				CWD:                 "/Users/admin/code/tokenmeter",
				GitBranch:           "main",
				LatestContextTokens: 33200,
				TotalCostUSD:        2.86,
				Model:               "sonnet",
				Status:              "active",
			},
		},
		height: 24,
	}

	view := m.viewDashboard(100)

	if strings.Contains(view, "Cache:") {
		t.Fatalf("dashboard preview should not show cache, got %q", view)
	}
	if strings.Contains(view, "CTX  STATUS") {
		t.Fatalf("dashboard table should no longer show ctx/status columns, got %q", view)
	}
	if !strings.Contains(view, "IN") || !strings.Contains(view, "OUT") {
		t.Fatalf("dashboard table should show input/output columns, got %q", view)
	}
	if strings.Contains(view, "In 527.4k") || strings.Contains(view, "Out 10.4k") {
		t.Fatalf("dashboard preview should not repeat input/output totals, got %q", view)
	}
	if !strings.Contains(view, "Ctx") || !strings.Contains(view, "Status") {
		t.Fatalf("dashboard preview should show ctx and status, got %q", view)
	}
	if strings.Contains(view, "Status ● run    Cost") {
		t.Fatalf("dashboard preview should stop showing cost, got %q", view)
	}
	if !strings.Contains(view, "/Users/admin/code/tokenmeter") {
		t.Fatalf("dashboard preview should show cwd path, got %q", view)
	}
}

func TestModelUpdateDashboardCyclesPlatformFilterAndCostSort(t *testing.T) {
	m := Model{
		activeTab: tabDashboard,
		sessions: []storage.SessionRow{
			{
				SessionID:    "claude-low",
				Platform:     "claude",
				GitBranch:    "claude-low",
				TotalCostUSD: 1.25,
			},
			{
				SessionID:    "codex-high",
				Platform:     "codex",
				GitBranch:    "codex-high",
				TotalCostUSD: 8.50,
			},
			{
				SessionID:    "claude-high",
				Platform:     "claude",
				GitBranch:    "claude-high",
				TotalCostUSD: 5.75,
			},
		},
		expandedCalls: make(map[string]bool),
		height:        24,
	}
	m.refreshFilteredViews()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	sorted := updated.(Model)
	if sorted.dashboardSort != sortCost {
		t.Fatalf("expected dashboard sort to switch to cost, got %v", sorted.dashboardSort)
	}
	if got := len(sorted.filteredSessions()); got != 3 {
		t.Fatalf("expected all sessions to remain visible after sort, got %d", got)
	}
	if sorted.filteredSessions()[0].SessionID != "codex-high" {
		t.Fatalf("expected highest-cost session first after cost sort, got %#v", sorted.filteredSessions())
	}
	if sorted.selectedSession != 1 {
		t.Fatalf("expected selected session to follow first sorted row, got %d", sorted.selectedSession)
	}

	updated, _ = sorted.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	filtered := updated.(Model)
	if filtered.platformFilter != platformClaude {
		t.Fatalf("expected platform filter to switch to Claude, got %v", filtered.platformFilter)
	}
	if got := len(filtered.filteredSessions()); got != 2 {
		t.Fatalf("expected only Claude sessions after platform filter, got %d", got)
	}
	if filtered.filteredSessions()[0].SessionID != "claude-high" || filtered.filteredSessions()[1].SessionID != "claude-low" {
		t.Fatalf("expected Claude sessions to stay cost-sorted, got %#v", filtered.filteredSessions())
	}
	if filtered.selectedSession != 2 {
		t.Fatalf("expected selected session to follow first filtered row, got %d", filtered.selectedSession)
	}
}

func TestPlatformBadgeUsesRequestedLabels(t *testing.T) {
	if got := platformBadge("claude"); !strings.Contains(got, "Claude") {
		t.Fatalf("expected claude badge to contain Claude, got %q", got)
	}
	if got := platformBadge("codex"); !strings.Contains(got, "Codex") {
		t.Fatalf("expected codex badge to contain Codex, got %q", got)
	}
	if got := platformBadge("codex"); strings.Contains(got, "CX") {
		t.Fatalf("expected codex badge to stop using CX, got %q", got)
	}
}

func TestViewDashboardLeftAlignsColumnsAfterBadge(t *testing.T) {
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:         "session-1",
				GitBranch:         "main",
				TotalInputTokens:  527400,
				TotalOutputTokens: 10400,
				TotalCostUSD:      2.86,
				Model:             "sonnet",
			},
		},
		filteredSessionsCache: []storage.SessionRow{
			{
				SessionID:         "session-1",
				GitBranch:         "main",
				TotalInputTokens:  527400,
				TotalOutputTokens: 10400,
				TotalCostUSD:      2.86,
				Model:             "sonnet",
				Status:            "active",
			},
		},
		height: 24,
	}

	view := m.viewDashboard(100)

	lines := strings.Split(view, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected dashboard view to include header and row, got %q", view)
	}

	headerLine := lines[2]
	rowLine := lines[3]

	sessionHeader := strings.Index(headerLine, "SESSION")
	sessionValue := strings.Index(rowLine, "main")
	badgeValue := strings.Index(rowLine, "Claude")
	costHeader := strings.Index(headerLine, "COST")
	costValue := strings.Index(rowLine, "$2.86")
	inHeader := strings.Index(headerLine, "IN")
	inValue := strings.Index(rowLine, "527.4k")
	outHeader := strings.Index(headerLine, "OUT")
	outValue := strings.Index(rowLine, "10.4k")

	if sessionHeader == -1 || sessionValue == -1 || badgeValue == -1 || costHeader == -1 || costValue == -1 || inHeader == -1 || inValue == -1 || outHeader == -1 || outValue == -1 {
		t.Fatalf("dashboard should include separate platform and left-aligned data columns, got %q", view)
	}
	if sessionHeader != sessionValue {
		t.Fatalf("expected SESSION column to align with session value, got header=%d value=%d in %q", sessionHeader, sessionValue, view)
	}
	if badgeValue >= sessionValue {
		t.Fatalf("expected platform badge to stay in its own leading column, got badge=%d session=%d in %q", badgeValue, sessionValue, view)
	}
	if costHeader != costValue {
		t.Fatalf("expected COST column to left-align with its value, got header=%d value=%d in %q", costHeader, costValue, view)
	}
	if inHeader != inValue {
		t.Fatalf("expected IN column to left-align with its value, got header=%d value=%d in %q", inHeader, inValue, view)
	}
	if outHeader != outValue {
		t.Fatalf("expected OUT column to left-align with its value, got header=%d value=%d in %q", outHeader, outValue, view)
	}
}

func TestDashboardUsesDistinctStylesForValuesAndClaudeBadge(t *testing.T) {
	oldProfile := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
	})

	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:         "session-1",
				Platform:          "claude",
				GitBranch:         "main",
				TotalInputTokens:  527400,
				TotalOutputTokens: 10400,
				TotalCostUSD:      2.86,
				Model:             "sonnet",
			},
		},
		filteredSessionsCache: []storage.SessionRow{
			{
				SessionID:         "session-1",
				Platform:          "claude",
				GitBranch:         "main",
				TotalInputTokens:  527400,
				TotalOutputTokens: 10400,
				TotalCostUSD:      2.86,
				Model:             "sonnet",
				Status:            "active",
			},
		},
		height: 24,
	}

	view := m.viewDashboard(100)
	headerColoredIn := headerStyle.Render(fmt.Sprintf("%-8s", "527.4k"))
	headerColoredOut := headerStyle.Render(fmt.Sprintf("%-8s", "10.4k"))
	headerColoredClaudeBadge := lipgloss.NewStyle().
		Width(dashboardBadgeWidth).
		Foreground(headerStyle.GetForeground()).
		Render("Claude")

	if strings.Contains(view, headerColoredIn) {
		t.Fatalf("expected IN values to stop using header style, got %q", view)
	}
	if strings.Contains(view, headerColoredOut) {
		t.Fatalf("expected OUT values to stop using header style, got %q", view)
	}
	if platformBadge("claude") == headerColoredClaudeBadge {
		t.Fatalf("expected Claude badge to stop using header color, got %q", platformBadge("claude"))
	}
	if !sameTerminalColor(dashboardMetricStyle.GetForeground(), colorInfo) {
		t.Fatalf("expected dashboard metric style to use colorInfo, got %#v", dashboardMetricStyle.GetForeground())
	}
	if !sameTerminalColor(claudeBadgeStyle.GetForeground(), colorClaudeBadge) {
		t.Fatalf("expected claude badge style to use colorClaudeBadge, got %#v", claudeBadgeStyle.GetForeground())
	}
	if sameTerminalColor(dashboardMetricStyle.GetForeground(), claudeBadgeStyle.GetForeground()) {
		t.Fatalf("expected dashboard metrics and claude badge to use distinct colors, got metric=%#v badge=%#v", dashboardMetricStyle.GetForeground(), claudeBadgeStyle.GetForeground())
	}
}

func sameTerminalColor(a, b lipgloss.TerminalColor) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
