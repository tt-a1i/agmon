package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// modelHarness builds a Model with splash dismissed and a seeded session,
// suitable for keyboard-event testing. Returns the model and the underlying
// DB (for further seeding inside specific tests).
func modelHarness(t *testing.T) (Model, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	m := NewModel(db, nil)
	m.splash = false
	m.width = 120
	m.height = 40
	m.activeTab = tabDashboard
	// Two sessions so [/] navigation and j/k have somewhere to go.
	m.sessions = []storage.SessionRow{
		{SessionID: "sess-A", Platform: "claude", Status: "active", StartTime: time.Now()},
		{SessionID: "sess-B", Platform: "codex", Status: "active", StartTime: time.Now().Add(-time.Minute)},
	}
	m.refreshFilteredViews()
	return m, db
}

func keyRunes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func sendKey(m Model, msg tea.KeyMsg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestUpdateQuitOnCtrlC(t *testing.T) {
	m, _ := modelHarness(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd, got nil")
	}
}

func TestUpdateTabCyclesForwardAndBackward(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabMessages {
		t.Errorf("after Tab from dashboard, activeTab = %d, want %d", m.activeTab, tabMessages)
	}

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.activeTab != tabDashboard {
		t.Errorf("after Shift-Tab, activeTab = %d, want %d", m.activeTab, tabDashboard)
	}

	// Shift-Tab from dashboard wraps to last tab (tabCount-1).
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.activeTab != tabCount-1 {
		t.Errorf("wraparound: activeTab = %d, want %d", m.activeTab, tabCount-1)
	}
}

func TestUpdateJDownIncrementsRowWithinBounds(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m.selectedRow = 0

	// 2 sessions visible → moves to row 1, stops there.
	m = sendKey(m, keyRunes('j'))
	if m.selectedRow != 1 {
		t.Errorf("after j, selectedRow = %d, want 1", m.selectedRow)
	}
	m = sendKey(m, keyRunes('j'))
	if m.selectedRow != 1 {
		t.Errorf("j past last row should stop, got %d", m.selectedRow)
	}
}

func TestUpdateKUpDecrementsRowDownToZero(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m.selectedRow = 1

	m = sendKey(m, keyRunes('k'))
	if m.selectedRow != 0 {
		t.Errorf("after k, selectedRow = %d, want 0", m.selectedRow)
	}
	m = sendKey(m, keyRunes('k'))
	if m.selectedRow != 0 {
		t.Errorf("k below 0 should stop at 0, got %d", m.selectedRow)
	}
}

func TestUpdateGJumpsToBottom(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m.selectedRow = 0

	m = sendKey(m, keyRunes('G'))
	if m.selectedRow != 1 {
		t.Errorf("after G, selectedRow = %d, want 1 (last)", m.selectedRow)
	}
}

func TestUpdateBracketNavigatesSessions(t *testing.T) {
	m, _ := modelHarness(t)
	m.selectedSession = 0

	m = sendKey(m, keyRunes(']'))
	if m.selectedSession != 1 {
		t.Errorf("after ], selectedSession = %d, want 1", m.selectedSession)
	}
	m = sendKey(m, keyRunes(']'))
	if m.selectedSession != 1 {
		t.Errorf("] past last should stop, got %d", m.selectedSession)
	}

	m = sendKey(m, keyRunes('['))
	if m.selectedSession != 0 {
		t.Errorf("after [, selectedSession = %d, want 0", m.selectedSession)
	}
}

func TestUpdateSlashEntersFilterMode(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard

	m = sendKey(m, keyRunes('/'))
	if !m.filterMode {
		t.Error("after /, filterMode should be true")
	}
}

func TestUpdateSlashOnStatsIsNoop(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabStats

	m = sendKey(m, keyRunes('/'))
	if m.filterMode {
		t.Error("stats tab should not enter filter mode on /")
	}
}

func TestUpdateEscClearsActiveFilter(t *testing.T) {
	m, _ := modelHarness(t)
	m.filterText = "abc"
	m.filterMode = false // active filter, but not typing

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filterText != "" {
		t.Errorf("Esc should clear filter, got %q", m.filterText)
	}
}

func TestUpdateFilterModeBackspaceShrinksText(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard

	// Enter filter mode and type some runes via Update.
	m = sendKey(m, keyRunes('/'))
	m, _ = func() (Model, tea.Cmd) {
		next, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})
		return next.(Model), c
	}()
	if m.filterText != "ab" {
		t.Fatalf("filterText after typing = %q, want ab", m.filterText)
	}

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filterText != "a" {
		t.Errorf("after backspace, filterText = %q, want a", m.filterText)
	}
}

func TestUpdateFilterModeEnterExits(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m = sendKey(m, keyRunes('/'))
	if !m.filterMode {
		t.Fatal("filterMode should be true after /")
	}

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filterMode {
		t.Errorf("Enter should exit filter mode (keep text), filterMode=%v", m.filterMode)
	}
}

func TestUpdateTCyclesSummaryRange(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	initial := m.summaryRange

	m = sendKey(m, keyRunes('t'))
	if m.summaryRange == initial {
		t.Errorf("t should cycle summaryRange, still %d", m.summaryRange)
	}
}

func TestUpdatePCyclesPlatformFilter(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	initial := m.platformFilter

	m = sendKey(m, keyRunes('p'))
	if m.platformFilter == initial {
		t.Errorf("p should cycle platformFilter")
	}
}

func TestUpdateSCyclesDashboardSort(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	initial := m.dashboardSort

	m = sendKey(m, keyRunes('s'))
	if m.dashboardSort == initial {
		t.Errorf("s should cycle dashboardSort")
	}
}

func TestUpdateUpperTCyclesTagFilter(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m.sessions = []storage.SessionRow{
		{SessionID: "refactor", Platform: "claude", Tag: "refactor", StartTime: time.Now()},
		{SessionID: "auth", Platform: "claude", Tag: "auth", StartTime: time.Now().Add(-time.Minute)},
		{SessionID: "plain", Platform: "codex", StartTime: time.Now().Add(-2 * time.Minute)},
	}
	m.refreshFilteredViews()

	m = sendKey(m, keyRunes('T'))
	if m.tagFilter != "auth" {
		t.Fatalf("first T should select sorted first tag auth, got %q", m.tagFilter)
	}
	if len(m.filteredSessions()) != 1 || m.filteredSessions()[0].SessionID != "auth" {
		t.Fatalf("auth filter returned %#v", m.filteredSessions())
	}

	m = sendKey(m, keyRunes('T'))
	if m.tagFilter != "refactor" {
		t.Fatalf("second T should select refactor, got %q", m.tagFilter)
	}

	m = sendKey(m, keyRunes('T'))
	if m.tagFilter != tagFilterUntagged {
		t.Fatalf("third T should select untagged, got %q", m.tagFilter)
	}

	m = sendKey(m, keyRunes('T'))
	if m.tagFilter != "" {
		t.Fatalf("fourth T should return to all, got %q", m.tagFilter)
	}
}

func TestModelWorkspaceToggleW(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m = m.WithWorkspace("/code/a")
	m.sessions = []storage.SessionRow{
		{SessionID: "workspace", Platform: "claude", CWD: "/code/a"},
		{SessionID: "other", Platform: "claude", CWD: "/code/b"},
	}
	m.refreshFilteredViews()

	if !m.workspaceFilter {
		t.Fatal("workspace filter should start enabled")
	}
	if got := len(m.filteredSessions()); got != 1 {
		t.Fatalf("workspace filter got %d sessions, want 1", got)
	}

	m = sendKey(m, keyRunes('W'))
	if m.workspaceFilter {
		t.Fatal("W should disable workspace filter")
	}
	if got := len(m.filteredSessions()); got != 2 {
		t.Fatalf("after W expected all sessions, got %d", got)
	}

	m = sendKey(m, keyRunes('W'))
	if !m.workspaceFilter {
		t.Fatal("second W should re-enable workspace filter")
	}
	if got := len(m.filteredSessions()); got != 1 {
		t.Fatalf("after second W expected workspace sessions, got %d", got)
	}
}

func TestModelWorkspaceShowAllA(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabDashboard
	m = m.WithWorkspace("/code/a")
	m.sessions = []storage.SessionRow{
		{SessionID: "workspace", Platform: "claude", CWD: "/code/a/sub"},
		{SessionID: "other", Platform: "claude", CWD: "/code/b"},
	}
	m.refreshFilteredViews()

	m = sendKey(m, keyRunes('A'))
	if m.workspaceFilter {
		t.Fatal("A should disable workspace filter")
	}
	if got := len(m.filteredSessions()); got != 2 {
		t.Fatalf("A should show all sessions, got %d", got)
	}
}

func TestUpdateUpperSGlobalSearchAndEnterOpensHit(t *testing.T) {
	m, db := modelHarness(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("search-hit", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-search", "", "search-hit", "Read", "needle params", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	m.sessions = append(m.sessions, storage.SessionRow{
		SessionID: "search-hit",
		Platform:  "claude",
		StartTime: now,
	})
	m.activeTab = tabMessages
	m.selectedSession = 0
	m.refreshFilteredViews()

	m = sendKey(m, keyRunes('S'))
	if !m.searchMode {
		t.Fatal("S should enter global search mode in detail view")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("needle")})
	m = updated.(Model)
	if m.searchText != "needle" {
		t.Fatalf("search text = %q, want needle", m.searchText)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.searchMode {
		t.Fatal("Enter should leave search input mode")
	}
	if len(m.searchHits) != 1 || m.searchHits[0].SessionID != "search-hit" {
		t.Fatalf("search hits = %#v", m.searchHits)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("opening a search hit should request a detail refresh")
	}
	if m.sessions[m.selectedSession].SessionID != "search-hit" {
		t.Fatalf("selected session = %q, want search-hit", m.sessions[m.selectedSession].SessionID)
	}
	if m.searchHits != nil {
		t.Fatalf("search popup should close after opening hit, got %#v", m.searchHits)
	}
}

func TestUpdateSEntersSearchModeInDetail(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabMessages

	m = sendKey(m, keyRunes('S'))

	if !m.searchMode {
		t.Fatal("S in detail view should enter search prompt mode")
	}
	if m.searchText != "" || m.searchHits != nil || m.searchSelected != 0 {
		t.Fatalf("search state should be reset on entry: text=%q hits=%#v selected=%d", m.searchText, m.searchHits, m.searchSelected)
	}
}

func TestUpdateSearchModeEnterTriggersDBSearch(t *testing.T) {
	m, db := modelHarness(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("search-edit-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("search-edit-call", "", "search-edit-session", "Edit", "Edit file auth.go", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	m.activeTab = tabMessages
	m = sendKey(m, keyRunes('S'))

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Edit")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.searchMode {
		t.Fatal("Enter should leave search prompt mode")
	}
	if m.searchText != "Edit" {
		t.Fatalf("searchText = %q, want Edit", m.searchText)
	}
	if len(m.searchHits) == 0 {
		t.Fatal("expected DB search to populate searchHits")
	}
	if m.searchHits[0].SessionID != "search-edit-session" {
		t.Fatalf("first hit = %#v, want search-edit-session", m.searchHits[0])
	}
}

func TestUpdateSearchModeEscClosesPopup(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabMessages
	m = sendKey(m, keyRunes('S'))
	m.searchText = "Edit"

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.searchMode || m.searchText != "" || m.searchHits != nil || m.searchErr != "" {
		t.Fatalf("Esc should close and reset search: mode=%v text=%q hits=%#v err=%q", m.searchMode, m.searchText, m.searchHits, m.searchErr)
	}
}

func TestUpdateSearchPopupJKNavigatesResults(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabMessages
	m.searchHits = []storage.SearchHit{
		{SessionID: "sess-A"},
		{SessionID: "sess-B"},
		{SessionID: "sess-C"},
	}
	m.searchSelected = 0

	m = sendKey(m, keyRunes('j'))
	if m.searchSelected != 1 {
		t.Fatalf("j should move to next search result, got %d", m.searchSelected)
	}
	m = sendKey(m, keyRunes('j'))
	m = sendKey(m, keyRunes('j'))
	if m.searchSelected != 2 {
		t.Fatalf("j should stop at last search result, got %d", m.searchSelected)
	}
	m = sendKey(m, keyRunes('k'))
	if m.searchSelected != 1 {
		t.Fatalf("k should move to previous search result, got %d", m.searchSelected)
	}
}

func TestUpdateSearchPopupEnterJumpsToSession(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabToolCalls
	m.selectedSession = 0
	m.searchHits = []storage.SearchHit{
		{SessionID: "sess-B"},
	}
	m.searchSelected = 0

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if cmd == nil {
		t.Fatal("Enter on search result should request detail refresh")
	}
	if m.activeTab != tabMessages {
		t.Fatalf("activeTab = %d, want messages", m.activeTab)
	}
	if m.selectedSession != 1 || m.sessions[m.selectedSession].SessionID != "sess-B" {
		t.Fatalf("selected session = %d (%s), want sess-B", m.selectedSession, m.sessions[m.selectedSession].SessionID)
	}
	if m.searchHits != nil || m.searchMode {
		t.Fatalf("search popup should close after jump: mode=%v hits=%#v", m.searchMode, m.searchHits)
	}
}

func TestUpdateEnterOnMessagesTogglesExpansion(t *testing.T) {
	m, _ := modelHarness(t)
	m.activeTab = tabMessages
	m.messages = []collector.UserMessage{{Timestamp: time.Now(), Content: "hello"}}
	m.refreshFilteredViews()
	m.selectedRow = 0

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.expandedCalls) != 1 {
		t.Errorf("after Enter, expandedCalls should have 1 entry, got %d", len(m.expandedCalls))
	}

	// Second Enter collapses (accordion).
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.expandedCalls) != 0 {
		t.Errorf("after second Enter, expandedCalls should be empty, got %d", len(m.expandedCalls))
	}
}

func TestUpdateSplashAnyKeyDismisses(t *testing.T) {
	m, _ := modelHarness(t)
	m.splash = true
	m.splashTick = 5

	m = sendKey(m, keyRunes('x'))
	if m.splash {
		t.Error("any key should dismiss splash")
	}
}

// TestUpdateCAttemptsCopyAndSetsFlashMsg covers the 'c' key path:
// resumeCommandForSession is built and either copied or surfaced via flashMsg.
// We can't reliably mock copyToClipboard in CI (no pbcopy/xclip on Linux
// runners), so we assert that the flash message is non-empty regardless of
// whether copy succeeded — both branches set a flash.
func TestUpdateCAttemptsCopyAndSetsFlashMsg(t *testing.T) {
	m, _ := modelHarness(t)
	m.selectedSession = 0

	m = sendKey(m, keyRunes('c'))

	if m.flashMsg == "" {
		t.Error("'c' key should set a flash message (copied or fallback)")
	}
	if m.flashExpire.IsZero() {
		t.Error("flashExpire should be set after 'c'")
	}
}

// TestUpdateRAttemptsShareReportFlash covers the 'r' key path that builds a
// share-recap Markdown and either copies it or shows the run command.
func TestUpdateRAttemptsShareReportFlash(t *testing.T) {
	m, _ := modelHarness(t)
	m.selectedSession = 0

	m = sendKey(m, keyRunes('r'))

	if m.flashMsg == "" {
		t.Error("'r' key should set a flash message")
	}
}

// TestUpdateCWithNoSelectionIsNoop verifies the guard that prevents reading
// past sessions[].
func TestUpdateCWithNoSelectionIsNoop(t *testing.T) {
	m, _ := modelHarness(t)
	m.sessions = nil // empty
	m.selectedSession = 0

	m = sendKey(m, keyRunes('c'))
	if m.flashMsg != "" {
		t.Errorf("with no sessions, 'c' should noop; got flashMsg=%q", m.flashMsg)
	}
}

// TestUpdateTickMsgQueuesRefreshAndTick exercises the tickMsg branch — it
// must return a non-nil Batch cmd so the loop continues.
func TestUpdateTickMsgQueuesRefreshAndTick(t *testing.T) {
	m, _ := modelHarness(t)
	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tickMsg must return non-nil cmd (tick+refresh batch)")
	}
}

// TestUpdateEventMsgQueuesListenAndRefresh exercises EventMsg branch.
func TestUpdateEventMsgQueuesListenAndRefresh(t *testing.T) {
	m, _ := modelHarness(t)
	_, cmd := m.Update(EventMsg{})
	if cmd == nil {
		t.Error("EventMsg must return non-nil cmd (listen+refresh batch)")
	}
}

// TestUpdateRefreshMsgRebuildsState covers refreshMsg dispatch.
func TestUpdateRefreshMsgRebuildsState(t *testing.T) {
	m, _ := modelHarness(t)
	next, _ := m.Update(refreshMsg{})
	if _, ok := next.(Model); !ok {
		t.Error("refreshMsg should return updated Model")
	}
}

// TestUpdateAvailableMsgSetsField verifies the upgrade-notification path.
func TestUpdateAvailableMsgSetsField(t *testing.T) {
	m, _ := modelHarness(t)
	next, _ := m.Update(UpdateAvailableMsg("1.2.3"))
	m = next.(Model)
	if m.updateAvailable != "1.2.3" {
		t.Errorf("updateAvailable = %q, want 1.2.3", m.updateAvailable)
	}
}

func TestUpdateWindowResizeUpdatesDimensions(t *testing.T) {
	m, _ := modelHarness(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = next.(Model)
	if m.width != 200 || m.height != 50 {
		t.Errorf("after resize, w=%d h=%d, want 200x50", m.width, m.height)
	}
}
