package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

// EventMsg signals new data is available from the daemon.
type EventMsg struct{}

type tab int

const (
	tabDashboard tab = iota
	tabMessages
	tabToolCalls
	tabStats
	tabCount // sentinel for modulo
)

type timeRange int

const (
	rangeToday timeRange = iota
	rangeWeek
	rangeMonth
	range3Month
	rangeYear
	rangeAll
	rangeCount
)

var rangeNames = []string{"Today", "Week", "Month", "3 Mon", "Year", "All"}

var tabNames = []string{"Dashboard", "Messages", "Tool Calls", "Stats"}

type sessionPlatformFilter int

const (
	platformAll sessionPlatformFilter = iota
	platformClaude
	platformCodex
	platformFilterCount
)

var platformFilterNames = []string{"All", "Claude", "Codex"}

type dashboardSort int

const (
	sortRecent dashboardSort = iota
	sortCost
	sortCount
)

var dashboardSortNames = []string{"Recent", "Cost"}

// rangeCutoff converts a timeRange to a *time.Time cutoff for DB queries.
// Returns nil for rangeAll (meaning no time filter).
func rangeCutoff(r timeRange) *time.Time {
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var t time.Time
	switch r {
	case rangeWeek:
		t = startOfDay.AddDate(0, 0, -7)
	case rangeMonth:
		t = startOfDay.AddDate(0, -1, 0)
	case range3Month:
		t = startOfDay.AddDate(0, -3, 0)
	case rangeYear:
		t = startOfDay.AddDate(-1, 0, 0)
	case rangeAll:
		return nil
	default: // rangeToday
		t = startOfDay
	}
	return &t
}

// contextWindowForModel returns the context window size for a given model name.
func contextWindowForModel(model string) int {
	switch {
	case strings.Contains(model, "opus"):
		return 1_000_000
	default: // sonnet, haiku, unknown
		return 200_000
	}
}

type Model struct {
	db                     *storage.DB
	eventCh                chan EventMsg
	splash                 bool // show splash screen on startup
	splashTick             int  // animation frame counter
	activeTab              tab
	sessions               []storage.SessionRow
	agents                 []storage.AgentRow
	toolCalls              []storage.ToolCallRow
	fileChanges            []storage.FileChangeRow
	toolStats              []storage.ToolStatRow
	agentStats             []storage.AgentStatRow
	messages               []collector.UserMessage
	messagesCacheID        string // session ID for which messages were loaded
	filteredSessionsCache  []storage.SessionRow
	filteredToolCallsCache []storage.ToolCallRow
	selectedSession        int
	selectedRow            int
	viewOffset             int
	expandedCalls          map[string]bool // call_id -> expanded
	summaryRange           timeRange
	platformFilter         sessionPlatformFilter
	dashboardSort          dashboardSort
	filterMode             bool
	filterText             string
	todayInput             int
	todayOutput            int
	todayCost              float64
	width                  int
	height                 int
	activeCount            int
	flashMsg               string
	flashExpire            time.Time
	statsLineCount         int    // total lines in stats view (for scrolling)
	updateAvailable        string // latest version if update available
	err                    error
}

type tickMsg time.Time
type refreshMsg struct{}

// UpdateAvailableMsg is sent when a newer version is found.
type UpdateAvailableMsg string

func NewModel(db *storage.DB, eventCh chan EventMsg) Model {
	return Model{
		db:            db,
		eventCh:       eventCh,
		expandedCalls: make(map[string]bool),
	}
}

func (m Model) Init() tea.Cmd {
	if m.splash {
		return splashTickCmd()
	}
	return tea.Batch(tickCmd(), listenEvents(m.eventCh), refreshCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func refreshCmd() tea.Cmd {
	return func() tea.Msg { return refreshMsg{} }
}

func listenEvents(ch chan EventMsg) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-ch
		if !ok {
			return nil
		}
		// Return EventMsg so Update re-queues listenEvents for the next event.
		// Returning refreshMsg here would stop the listener after the first event.
		return EventMsg{}
	}
}

// visibleRows returns how many list rows fit in the content area.
func (m Model) visibleRows() int {
	v := m.height - 12
	if v < 5 {
		return 5
	}
	return v
}

// --- Filtered views ---

func (m Model) filteredSessions() []storage.SessionRow {
	return m.filteredSessionsCache
}

func (m Model) filteredToolCalls() []storage.ToolCallRow {
	return m.filteredToolCallsCache
}

func (m Model) currentTabRowCount() int {
	switch m.activeTab {
	case tabDashboard:
		return len(m.filteredSessions())
	case tabMessages:
		return len(m.messages)
	case tabToolCalls:
		return len(m.filteredToolCalls())
	case tabStats:
		return m.statsLineCount
	}
	return 0
}

// tabVisibleRows returns how many data rows actually fit in the current tab's
// rendered area, accounting for headers/summaries each tab reserves.
func (m Model) tabVisibleRows() int {
	base := m.visibleRows()
	switch m.activeTab {
	case tabDashboard:
		return base - 6 // summary + header + "more" hint + blank + preview bar
	case tabMessages:
		return base - 3
	case tabToolCalls:
		return base - 2
	default: // tabStats
		return base
	}
}

// adjustScroll keeps selectedRow visible within the viewport.
func (m *Model) adjustScroll() {
	visible := m.tabVisibleRows()
	if visible < 1 {
		visible = 1
	}
	if m.selectedRow < m.viewOffset {
		m.viewOffset = m.selectedRow
	} else if m.selectedRow >= m.viewOffset+visible {
		m.viewOffset = m.selectedRow - visible + 1
	}
	if m.viewOffset < 0 {
		m.viewOffset = 0
	}
}

// clearMsgExpanded removes message expansion state (keyed by index, not content).
func (m *Model) clearMsgExpanded() {
	for k := range m.expandedCalls {
		if strings.HasPrefix(k, "msg-") {
			delete(m.expandedCalls, k)
		}
	}
}

// syncSessionFromRow updates selectedSession to match the current selectedRow
// on the Dashboard tab. This keeps them in sync when the user navigates with j/k.
func (m *Model) syncSessionFromRow() {
	filtered := m.filteredSessions()
	if m.selectedRow < len(filtered) {
		targetID := filtered[m.selectedRow].SessionID
		for i, s := range m.sessions {
			if s.SessionID == targetID {
				m.selectedSession = i
				return
			}
		}
	}
}

// syncRowFromSelectedSession updates selectedRow to match the current
// selectedSession within the dashboard's filtered/sorted list.
func (m *Model) syncRowFromSelectedSession() bool {
	if m.selectedSession >= len(m.sessions) {
		return false
	}
	targetID := m.sessions[m.selectedSession].SessionID
	filtered := m.filteredSessions()
	for i, s := range filtered {
		if s.SessionID == targetID {
			m.selectedRow = i
			return true
		}
	}
	return false
}

// restoreTabCursor sets selectedRow and viewOffset when switching tabs.
// For Dashboard, it restores the cursor to the currently selected session.
// For other tabs, it resets to the top.
// Filter text is always cleared since it is tab-specific.
func (m *Model) restoreTabCursor() {
	m.resetFilter()

	if m.activeTab == tabDashboard {
		// Restore cursor to the selected session's position inside the current
		// filtered/sorted dashboard view.
		if m.selectedSession < len(m.sessions) && m.syncRowFromSelectedSession() {
			m.adjustScroll()
			return
		}
		m.selectedRow = 0
		m.viewOffset = 0
	} else {
		m.selectedRow = 0
		m.viewOffset = 0
	}
}

type splashTickMsg struct{}

func splashTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return splashTickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Splash screen: any key dismisses, ticks animate
	if m.splash {
		switch msg.(type) {
		case tea.KeyMsg:
			m.splash = false
			return m, tea.Batch(refreshCmd(), tickCmd(), listenEvents(m.eventCh))
		case tea.WindowSizeMsg:
			wmsg := msg.(tea.WindowSizeMsg)
			m.width = wmsg.Width
			m.height = wmsg.Height
			return m, nil
		case splashTickMsg:
			m.splashTick++
			if m.splashTick >= 20 { // auto-dismiss after ~1.6s
				m.splash = false
				return m, tea.Batch(refreshCmd(), tickCmd(), listenEvents(m.eventCh))
			}
			return m, splashTickCmd()
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Filter mode: capture text input; only a few keys have special meaning.
		if m.filterMode {
			switch msg.Type {
			case tea.KeyCtrlC:
				return m, tea.Quit
			case tea.KeyEsc:
				m.resetFilter()
				m.resetListPosition()
				return m, nil
			case tea.KeyEnter:
				// Confirm filter, exit typing mode (filter stays active).
				m.filterMode = false
				m.resetListPosition()
				return m, nil
			case tea.KeyBackspace, tea.KeyDelete:
				if len(m.filterText) > 0 {
					m.setFilterText(m.filterText[:len(m.filterText)-1])
					m.resetListPosition()
				}
				return m, nil
			case tea.KeyRunes:
				m.setFilterText(m.filterText + string(msg.Runes))
				m.resetListPosition()
				return m, nil
			}
			return m, nil
		}

		// Normal mode.
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("/"))):
			if m.activeTab == tabMessages || m.activeTab == tabStats {
				return m, nil // Messages and Stats tabs do not support filtering
			}
			// Enter filter mode; pressing / again clears the existing filter.
			m.filterMode = true
			m.setFilterText("")
			m.resetListPosition()
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			// Clear active filter without entering filter mode.
			if m.filterText != "" {
				m.setFilterText("")
				m.resetListPosition()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			m.activeTab = (m.activeTab + 1) % tabCount
			m.restoreTabCursor()
			return m, refreshCmd()

		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			m.activeTab = (m.activeTab + tabCount - 1) % tabCount
			m.restoreTabCursor()
			return m, refreshCmd()

		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if count := m.currentTabRowCount(); count > 0 && m.selectedRow < count-1 {
				m.selectedRow++
				m.adjustScroll()
				if m.activeTab == tabDashboard {
					m.syncSessionFromRow()
				}
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.selectedRow > 0 {
				m.selectedRow--
				m.adjustScroll()
				if m.activeTab == tabDashboard {
					m.syncSessionFromRow()
				}
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
			// Jump to bottom
			if count := m.currentTabRowCount(); count > 0 {
				m.selectedRow = count - 1
				m.adjustScroll()
				if m.activeTab == tabDashboard {
					m.syncSessionFromRow()
				}
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("["))):
			if m.selectedSession > 0 {
				m.selectedSession--
				m.clearMsgExpanded()
				m.setFilterText("")
				if m.activeTab == tabDashboard {
					if !m.syncRowFromSelectedSession() {
						m.selectedRow = 0
					}
					m.adjustScroll()
				} else {
					m.resetListPosition()
				}
				return m, refreshCmd()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("]"))):
			if m.selectedSession < len(m.sessions)-1 {
				m.selectedSession++
				m.clearMsgExpanded()
				m.setFilterText("")
				if m.activeTab == tabDashboard {
					if !m.syncRowFromSelectedSession() {
						m.selectedRow = 0
					}
					m.adjustScroll()
				} else {
					m.resetListPosition()
				}
				return m, refreshCmd()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.activeTab == tabDashboard {
				filtered := m.filteredSessions()
				if m.selectedRow < len(filtered) {
					// Map filtered index back to unfiltered sessions index.
					targetID := filtered[m.selectedRow].SessionID
					for i, s := range m.sessions {
						if s.SessionID == targetID {
							m.selectedSession = i
							break
						}
					}
					// Go to Messages — see what was discussed in this session.
					m.activeTab = tabMessages
					m.resetListPosition()
					m.resetFilter()
					return m, refreshCmd()
				}
			}
			if m.activeTab == tabMessages {
				if m.selectedRow < len(m.messages) {
					key := fmt.Sprintf("msg-%d", m.selectedRow)
					wasOpen := m.expandedCalls[key]
					// Accordion: close all, then toggle this one
					m.clearMsgExpanded()
					if !wasOpen {
						m.expandedCalls[key] = true
					}
				}
				return m, nil
			}
			if m.activeTab == tabToolCalls {
				filtered := m.filteredToolCalls()
				if m.selectedRow < len(filtered) {
					tc := filtered[m.selectedRow]
					wasOpen := m.expandedCalls[tc.CallID]
					// Accordion: close all tool expansions, then toggle this one
					m.clearToolExpanded()
					if !wasOpen {
						m.expandedCalls[tc.CallID] = true
					}
				}
				return m, nil
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("c"))):
			if m.selectedSession < len(m.sessions) {
				s := m.sessions[m.selectedSession]
				cmd := fmt.Sprintf("claude --resume %s", s.SessionID)
				name := sessionDisplayName(s)
				if err := copyToClipboard(cmd); err == nil {
					m.flashMsg = fmt.Sprintf("Copied resume cmd for %s", name)
				} else {
					m.flashMsg = fmt.Sprintf("Run: %s", cmd)
				}
				m.flashExpire = time.Now().Add(3 * time.Second)
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("t"))):
			if m.activeTab == tabDashboard {
				m.summaryRange = (m.summaryRange + 1) % rangeCount
				return m, refreshCmd()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("p"))):
			if m.activeTab == tabDashboard {
				m.platformFilter = (m.platformFilter + 1) % platformFilterCount
				m.refreshFilteredViews()
				m.resetListPosition()
				m.syncSessionFromRow()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
			if m.activeTab == tabDashboard {
				m.dashboardSort = (m.dashboardSort + 1) % sortCount
				m.refreshFilteredViews()
				m.resetListPosition()
				m.syncSessionFromRow()
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustScroll()
		return m, nil

	case tickMsg:
		return m, tea.Batch(tickCmd(), refreshCmd())

	case EventMsg:
		return m, tea.Batch(listenEvents(m.eventCh), refreshCmd())

	case refreshMsg:
		m.refresh()
		return m, nil

	case UpdateAvailableMsg:
		m.updateAvailable = string(msg)
		return m, nil
	}

	return m, nil
}

func (m *Model) refresh() {
	m.sessions, m.err = m.db.ListSessions()
	m.activeCount, _ = m.db.GetActiveSessionCount()

	cutoff := rangeCutoff(m.summaryRange)
	m.todayInput, m.todayOutput, _ = m.db.GetTokensSince(cutoff)
	m.todayCost, _ = m.db.GetCostSince(cutoff)

	if len(m.sessions) > 0 {
		if m.selectedSession >= len(m.sessions) {
			m.selectedSession = 0
		}
		s := m.sessions[m.selectedSession]
		sid := s.SessionID
		m.agents, _ = m.db.ListAgents(sid)
		m.toolCalls, _ = m.db.ListToolCalls(sid, 500)
		m.fileChanges, _ = m.db.ListFileChanges(sid)
		m.toolStats, _ = m.db.ListToolStats(sid)
		m.agentStats, _ = m.db.ListAgentStats(sid)
		m.statsLineCount = len(m.buildStatsLines(m.width - 4))
		// Load user messages. Cache by session ID, but always reload for active sessions.
		if m.messagesCacheID != sid || s.Status == "active" {
			m.messages = collector.ReadUserMessages(event.Platform(s.Platform), sid, s.CWD, 200)
			m.messagesCacheID = sid
		}
	} else {
		m.agents = nil
		m.toolCalls = nil
		m.fileChanges = nil
		m.toolStats = nil
		m.agentStats = nil
		m.statsLineCount = 0
		m.messages = nil
		m.messagesCacheID = ""
	}

	m.refreshFilteredViews()
	m.pruneExpandedCalls()
	if m.activeTab == tabDashboard {
		if !m.syncRowFromSelectedSession() {
			if len(m.filteredSessionsCache) > 0 {
				m.selectedRow = 0
				m.syncSessionFromRow()
			} else {
				m.selectedRow = 0
			}
		}
	}

	// Clamp selectedRow to prevent stale-index panics.
	if count := m.currentTabRowCount(); m.selectedRow >= count {
		if count > 0 {
			m.selectedRow = count - 1
		} else {
			m.selectedRow = 0
		}
	}
	m.adjustScroll()
}

func sessionDisplayName(s storage.SessionRow) string {
	project := ""
	if s.CWD != "" {
		project = filepath.Base(s.CWD)
	}
	branch := s.GitBranch

	if project != "" && branch != "" {
		return project + "/" + branch
	}
	if project != "" {
		return project
	}
	if branch != "" {
		return branch
	}
	if len(s.SessionID) > 20 {
		return s.SessionID[:20]
	}
	return s.SessionID
}
