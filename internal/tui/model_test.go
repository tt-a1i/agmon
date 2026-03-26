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

func TestViewDashboardMovesCtxStatusToPreviewAndShowsPath(t *testing.T) {
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:                "session-1",
				CWD:                      "/Users/admin/code/agmon",
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
				CWD:                 "/Users/admin/code/agmon",
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
	if !strings.Contains(view, "/Users/admin/code/agmon") {
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
	if got := platformBadge("claude"); !strings.Contains(got, "CC") {
		t.Fatalf("expected claude badge to contain CC, got %q", got)
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
	badgeValue := strings.Index(rowLine, "CC")
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
		Render("CC")

	if strings.Contains(view, headerColoredIn) {
		t.Fatalf("expected IN values to stop using header style, got %q", view)
	}
	if strings.Contains(view, headerColoredOut) {
		t.Fatalf("expected OUT values to stop using header style, got %q", view)
	}
	if platformBadge("claude") == headerColoredClaudeBadge {
		t.Fatalf("expected CC badge to stop using header color, got %q", platformBadge("claude"))
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
