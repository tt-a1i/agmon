package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/storage"
)

// EventMsg signals new data is available from the daemon.
type EventMsg struct{}

type tab int

const (
	tabDashboard tab = iota
	tabMessages
	tabToolCalls
	tabTimeline
	tabCount // sentinel for modulo
)

type timeRange int

const (
	rangeToday timeRange = iota
	rangeWeek
	rangeMonth
	rangeAll
	rangeCount
)

var rangeNames = []string{"Today", "Week", "Month", "All"}

var tabNames = []string{"Dashboard", "Messages", "Tool Calls", "Timeline"}

// contextWindowForModel returns the context window size for a given model name.
func contextWindowForModel(model string) int {
	switch {
	case strings.Contains(model, "opus"):
		return 1_000_000
	default: // sonnet, haiku, unknown
		return 200_000
	}
}

type timelineEntry struct {
	time   time.Time
	kind   string
	detail string
	status string
}

type Model struct {
	db              *storage.DB
	eventCh         chan EventMsg
	splash          bool // show splash screen on startup
	splashTick      int  // animation frame counter
	activeTab       tab
	sessions        []storage.SessionRow
	agents          []storage.AgentRow
	toolCalls       []storage.ToolCallRow
	fileChanges     []storage.FileChangeRow
	timelineEntries []timelineEntry
	messages        []collector.UserMessage
	messagesCacheID string // session ID for which messages were loaded
	selectedSession int
	selectedRow     int
	viewOffset      int
	expandedCalls   map[string]bool // call_id -> expanded
	summaryRange    timeRange
	filterMode      bool
	filterText      string
	todayInput      int
	todayOutput     int
	todayCost       float64
	width           int
	height          int
	activeCount     int
	flashMsg        string
	flashExpire     time.Time
	err             error
}

type tickMsg time.Time
type refreshMsg struct{}

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
		return refreshMsg{}
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
	if m.filterText == "" {
		return m.sessions
	}
	f := strings.ToLower(m.filterText)
	var out []storage.SessionRow
	for _, s := range m.sessions {
		if strings.Contains(strings.ToLower(sessionDisplayName(s)), f) ||
			strings.Contains(strings.ToLower(s.Platform), f) {
			out = append(out, s)
		}
	}
	return out
}


func (m Model) filteredToolCalls() []storage.ToolCallRow {
	if m.filterText == "" {
		return m.toolCalls
	}
	f := strings.ToLower(m.filterText)
	var out []storage.ToolCallRow
	for _, tc := range m.toolCalls {
		if strings.Contains(strings.ToLower(tc.ToolName), f) ||
			strings.Contains(strings.ToLower(tc.ParamsSummary), f) {
			out = append(out, tc)
		}
	}
	return out
}

func (m Model) filteredTimeline() []timelineEntry {
	if m.filterText == "" {
		return m.timelineEntries
	}
	f := strings.ToLower(m.filterText)
	var out []timelineEntry
	for _, e := range m.timelineEntries {
		if strings.Contains(strings.ToLower(e.detail), f) ||
			strings.Contains(strings.ToLower(e.kind), f) {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) currentTabRowCount() int {
	switch m.activeTab {
	case tabDashboard:
		return len(m.filteredSessions())
	case tabMessages:
		return len(m.messages)
	case tabToolCalls:
		return len(m.filteredToolCalls())
	case tabTimeline:
		return len(m.filteredTimeline())
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
	default: // tabTimeline
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

// restoreTabCursor sets selectedRow and viewOffset when switching tabs.
// For Dashboard, it restores the cursor to the currently selected session.
// For other tabs, it resets to the top.
// Filter text is always cleared since it is tab-specific.
func (m *Model) restoreTabCursor() {
	m.filterText = ""
	m.filterMode = false

	if m.activeTab == tabDashboard {
		// Restore cursor to the selectedSession position.
		if m.selectedSession < len(m.sessions) {
			m.selectedRow = m.selectedSession
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
				m.filterMode = false
				m.filterText = ""
				m.selectedRow = 0
				m.viewOffset = 0
				return m, nil
			case tea.KeyEnter:
				// Confirm filter, exit typing mode (filter stays active).
				m.filterMode = false
				m.selectedRow = 0
				m.viewOffset = 0
				return m, nil
			case tea.KeyBackspace, tea.KeyDelete:
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.selectedRow = 0
					m.viewOffset = 0
				}
				return m, nil
			case tea.KeyRunes:
				m.filterText += string(msg.Runes)
				m.selectedRow = 0
				m.viewOffset = 0
				return m, nil
			}
			return m, nil
		}

		// Normal mode.
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("/"))):
			if m.activeTab == tabMessages {
				return m, nil // Messages tab does not support filtering
			}
			// Enter filter mode; pressing / again clears the existing filter.
			m.filterMode = true
			m.filterText = ""
			m.selectedRow = 0
			m.viewOffset = 0
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			// Clear active filter without entering filter mode.
			if m.filterText != "" {
				m.filterText = ""
				m.selectedRow = 0
				m.viewOffset = 0
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
				m.filterText = ""
				if m.activeTab == tabDashboard {
					m.selectedRow = m.selectedSession
					m.adjustScroll()
				} else {
					m.selectedRow = 0
					m.viewOffset = 0
				}
				return m, refreshCmd()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("]"))):
			if m.selectedSession < len(m.sessions)-1 {
				m.selectedSession++
				m.clearMsgExpanded()
				m.filterText = ""
				if m.activeTab == tabDashboard {
					m.selectedRow = m.selectedSession
					m.adjustScroll()
				} else {
					m.selectedRow = 0
					m.viewOffset = 0
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
					m.selectedRow = 0
					m.viewOffset = 0
					m.filterText = ""
					m.filterMode = false
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
					for k := range m.expandedCalls {
						if !strings.HasPrefix(k, "msg-") {
							delete(m.expandedCalls, k)
						}
					}
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
	}

	return m, nil
}

func (m *Model) refresh() {
	m.sessions, m.err = m.db.ListSessions()
	m.activeCount, _ = m.db.GetActiveSessionCount()

	switch m.summaryRange {
	case rangeWeek:
		m.todayInput, m.todayOutput, _ = m.db.GetWeekTokens()
		m.todayCost, _ = m.db.GetWeekCost()
	case rangeMonth:
		m.todayInput, m.todayOutput, _ = m.db.GetMonthTokens()
		m.todayCost, _ = m.db.GetMonthCost()
	case rangeAll:
		m.todayInput, m.todayOutput, _ = m.db.GetAllTokens()
		m.todayCost, _ = m.db.GetAllCost()
	default:
		m.todayInput, m.todayOutput, _ = m.db.GetTodayTokens()
		m.todayCost, _ = m.db.GetTodayCost()
	}

	if len(m.sessions) > 0 {
		if m.selectedSession >= len(m.sessions) {
			m.selectedSession = 0
		}
		s := m.sessions[m.selectedSession]
		sid := s.SessionID
		m.agents, _ = m.db.ListAgents(sid)
		m.toolCalls, _ = m.db.ListToolCalls(sid, 500)
		m.fileChanges, _ = m.db.ListFileChanges(sid)
		m.timelineEntries = buildTimeline(m.agents, m.toolCalls, m.fileChanges)
		// Load user messages. Cache by session ID, but always reload for active sessions.
		if m.messagesCacheID != sid || s.Status == "active" {
			m.messages = collector.ReadUserMessages(sid, s.CWD, 200)
			m.messagesCacheID = sid
		}
	} else {
		m.agents = nil
		m.toolCalls = nil
		m.fileChanges = nil
		m.messages = nil
		m.messagesCacheID = ""
		m.timelineEntries = nil
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

func buildTimeline(agents []storage.AgentRow, toolCalls []storage.ToolCallRow, fileChanges []storage.FileChangeRow) []timelineEntry {
	var entries []timelineEntry

	for _, a := range agents {
		role := a.Role
		if role == "" {
			role = "agent"
		}
		entries = append(entries, timelineEntry{
			time:   a.StartTime,
			kind:   "agent",
			detail: fmt.Sprintf("spawn %s", role),
			status: "start",
		})
		if a.EndTime != nil {
			entries = append(entries, timelineEntry{
				time:   *a.EndTime,
				kind:   "agent",
				detail: fmt.Sprintf("%s complete", role),
				status: "end",
			})
		}
	}

	for _, tc := range toolCalls {
		entries = append(entries, timelineEntry{
			time:   tc.StartTime,
			kind:   "tool",
			detail: fmt.Sprintf("%s %s", tc.ToolName, truncate(tc.ParamsSummary, 40)),
			status: tc.Status,
		})
	}

	for _, fc := range fileChanges {
		entries = append(entries, timelineEntry{
			time:   fc.Timestamp,
			kind:   "file",
			detail: fmt.Sprintf("%s %s", fc.ChangeType, fc.FilePath),
			status: "ok",
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].time.Before(entries[j].time)
	})
	return entries
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

const splashLogo = `
     ██████╗  ██████╗ ███╗   ███╗ ██████╗ ███╗   ██╗
    ██╔══██╗██╔════╝ ████╗ ████║██╔═══██╗████╗  ██║
    ███████║██║  ███╗██╔████╔██║██║   ██║██╔██╗ ██║
    ██╔══██║██║   ██║██║╚██╔╝██║██║   ██║██║╚██╗██║
    ██║  ██║╚██████╔╝██║ ╚═╝ ██║╚██████╔╝██║ ╚████║
    ╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.splash {
		return m.viewSplash()
	}

	var b strings.Builder

	// Tabs
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render(name))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(name))
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n")

	// Content
	contentWidth := m.width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	var content string
	switch m.activeTab {
	case tabDashboard:
		content = m.viewDashboard(contentWidth)
	case tabMessages:
		content = m.viewMessages(contentWidth)
	case tabToolCalls:
		content = m.viewToolCalls(contentWidth)
	case tabTimeline:
		content = m.viewTimeline(contentWidth)
	}

	b.WriteString(boxStyle.Width(contentWidth).Render(content))

	// Footer
	b.WriteString("\n")
	var footer string
	if m.flashMsg != "" && time.Now().Before(m.flashExpire) {
		footer = " " + flashStyle.Render("✓ "+m.flashMsg)
	} else if m.filterMode {
		footer = fmt.Sprintf(" %s %s%s  %s  %s",
			filterLabelStyle.Render("Filter:"),
			filterInputStyle.Render(m.filterText),
			filterLabelStyle.Render("█"),
			fmtKey("Esc", "cancel"),
			fmtKey("Enter", "confirm"))
	} else if m.filterText != "" {
		footer = fmt.Sprintf(" %s %s  %s  %s  %s",
			filterLabelStyle.Render("Filter:"),
			filterInputStyle.Render(m.filterText),
			fmtKey("Esc", "clear"),
			fmtKey("Tab", "view"),
			fmtKey("q", "quit"))
	} else if m.activeTab == tabDashboard {
		footer = fmt.Sprintf(" %s  %s  %s  %s  %s  %s  %s",
			fmtKey("t", "range"),
			fmtKey("/", "filter"),
			fmtKey("Tab", "view"),
			fmtKey("j/k", "nav"),
			fmtKey("c", "copy"),
			fmtKey("Enter", "msgs"),
			fmtKey("q", "quit"))
	} else {
		// Detail tabs: Messages, Tool Calls, Timeline
		pos := ""
		if len(m.sessions) > 1 {
			pos = fmt.Sprintf("  %s",
				mutedStyle.Render(fmt.Sprintf("session %d/%d", m.selectedSession+1, len(m.sessions))))
		}
		enterHint := ""
		if m.activeTab == tabMessages || m.activeTab == tabToolCalls {
			enterHint = fmt.Sprintf("  %s", fmtKey("Enter", "expand"))
		}
		filterHint := ""
		if m.activeTab != tabMessages {
			filterHint = fmt.Sprintf("  %s", fmtKey("/", "filter"))
		}
		footer = fmt.Sprintf(" %s  %s  %s  %s%s%s  %s%s",
			fmtKey("Tab", "view"),
			fmtKey("[/]", "session"),
			fmtKey("j/k", "nav"),
			fmtKey("c", "copy"),
			enterHint,
			filterHint,
			fmtKey("q", "quit"),
			pos)
	}
	if m.err != nil {
		footer += "  " + errorStyle.Render("err: "+m.err.Error())
	}
	b.WriteString(footer)

	return b.String()
}

func (m Model) viewSplash() string {
	var b strings.Builder

	// Center vertically
	padTop := (m.height - 10) / 2
	if padTop < 0 {
		padTop = 0
	}
	for i := 0; i < padTop; i++ {
		b.WriteString("\n")
	}

	// Reveal logo lines progressively
	lines := strings.Split(splashLogo, "\n")
	for i, line := range lines {
		if i == 0 && line == "" {
			continue
		}
		if m.splashTick >= i {
			b.WriteString(titleStyle.Render(line) + "\n")
		}
	}

	// Subtitle appears after logo
	if m.splashTick >= 8 {
		b.WriteString("\n")
		sub := mutedStyle.Render("          AI Agent Monitor — cost, context, control")
		b.WriteString(sub + "\n")
	}
	if m.splashTick >= 12 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("                    press any key to start") + "\n")
	}

	return b.String()
}

func (m Model) viewDashboard(width int) string {
	var b strings.Builder

	// Summary bar
	rangeName := rangeNames[m.summaryRange]
	b.WriteString(fmt.Sprintf(" %s %s %s %s    %s %s\n\n",
		mutedStyle.Render(rangeName+" In"), headerStyle.Render(formatTokens(m.todayInput)),
		mutedStyle.Render("/ Out"), headerStyle.Render(formatTokens(m.todayOutput)),
		mutedStyle.Render("Cost"), costStyle.Render(fmt.Sprintf("$%.2f", m.todayCost))))

	filtered := m.filteredSessions()
	if len(filtered) == 0 {
		if len(m.sessions) == 0 {
			msg := "  No sessions yet."
			if !claudeHooksConfigured() {
				msg += "\n\n  Run 'agmon setup' to configure Claude Code hooks,\n  then start using Claude Code normally."
			} else {
				msg += "\n  Start using Claude Code or Codex to see data."
			}
			b.WriteString(mutedStyle.Render(msg))
		} else {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No sessions match %q", m.filterText)))
		}
		return b.String()
	}

	hdr := fmt.Sprintf("  %-22s %-14s  %8s  %7s  %s", "SESSION", "STARTED", "COST", "CTX", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		s := filtered[i]
		status := lipgloss.NewStyle().Foreground(colorSuccess).Render("● run")
		switch s.Status {
		case "ended":
			status = mutedStyle.Render("  end")
		case "stale":
			status = mutedStyle.Render("  ---")
		}

		badge := platformBadge(s.Platform)
		name := displayTruncate(sessionDisplayName(s), 18)

		started := formatStartTime(s.StartTime)
		// Pad plain text first, then apply color — ANSI codes break %-Ns alignment.
		costText := fmt.Sprintf("$%.2f", s.TotalCostUSD)
		if s.TotalCostUSD < 0.005 {
			costText = "-"
		}
		costPad := fmt.Sprintf("%8s", costText)
		ctxText := formatTokens(s.LatestContextTokens)
		if s.LatestContextTokens == 0 {
			ctxText = "-"
		}
		ctxPad := fmt.Sprintf("%7s", ctxText)

		// badge is 2 visible chars + ANSI, pad name to 18 so badge+space+name = 22 cols
		line := fmt.Sprintf("  %s %-18s %s  %s  %s  %s",
			badge, name,
			mutedStyle.Render(fmt.Sprintf("%-14s", started)),
			costStyle.Render(costPad),
			contextColorize(s.LatestContextTokens, s.Model, ctxPad),
			status)

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more (j to scroll)", len(filtered)-end)) + "\n")
	}

	// Selected session preview bar
	if m.selectedSession < len(m.sessions) {
		s := m.sessions[m.selectedSession]
		b.WriteString("\n")
		preview := fmt.Sprintf(" %s %s    %s %s    %s %s    %s %s    %s %s",
			mutedStyle.Render("▸"), headerStyle.Render(sessionDisplayName(s)),
			mutedStyle.Render("In"), headerStyle.Render(formatTokens(s.TotalInputTokens)),
			mutedStyle.Render("Out"), headerStyle.Render(formatTokens(s.TotalOutputTokens)),
			mutedStyle.Render("Ctx"), contextPercent(s.LatestContextTokens, s.Model),
			mutedStyle.Render("Cost"), costStyle.Render(fmt.Sprintf("$%.2f", s.TotalCostUSD)))
		if cr := cacheHitRate(s); cr != "" {
			preview += "    " + mutedStyle.Render(cr)
		}
		b.WriteString(preview)
	}

	return b.String()
}



func (m Model) viewMessages(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(sessionHeader(s) + "\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d messages", len(m.messages))) + "\n\n")

	if len(m.messages) == 0 {
		if s.CWD == "" {
			b.WriteString(mutedStyle.Render("  No messages (session has no CWD — Codex sessions not supported yet)"))
		} else {
			b.WriteString(mutedStyle.Render("  No user messages found"))
		}
		return b.String()
	}

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(m.messages) {
		end = len(m.messages)
	}

	for i := start; i < end; i++ {
		msg := m.messages[i]
		timeStr := msg.Timestamp.Format("15:04")
		expanded := m.expandedCalls[fmt.Sprintf("msg-%d", i)]

		if expanded {
			line := fmt.Sprintf("  %s  %s %s",
				mutedStyle.Render(timeStr),
				msgPromptStyle.Render("▼"),
				msgTextStyle.Render(displayTruncate(strings.ReplaceAll(msg.Content, "\n", " "), width-14)))
			if i == m.selectedRow {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
			// Render full content with word wrap
			for _, rawLine := range strings.Split(msg.Content, "\n") {
				rawLine = strings.TrimSpace(rawLine)
				if rawLine == "" {
					continue
				}
				// Wrap long lines into multiple display lines
				for len(rawLine) > 0 {
					chunk := displayTruncate(rawLine, width-10)
					actual := chunk
					if strings.HasSuffix(chunk, "...") {
						actual = chunk[:len(chunk)-3]
					}
					b.WriteString(mutedStyle.Render("         "+actual) + "\n")
					if len(actual) >= len(rawLine) {
						break
					}
					rawLine = rawLine[len(actual):]
				}
			}
		} else {
			content := strings.ReplaceAll(msg.Content, "\n", " ")
			content = displayTruncate(content, width-14)
			line := fmt.Sprintf("  %s  %s %s",
				mutedStyle.Render(timeStr),
				msgPromptStyle.Render(">"),
				msgTextStyle.Render(content))
			if i == m.selectedRow {
				line = selectedStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	if end < len(m.messages) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more (j to scroll)", len(m.messages)-end)) + "\n")
	}

	return b.String()
}

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

		if m.expandedCalls[tc.CallID] {
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
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more", len(filtered)-end)) + "\n")
	}

	return b.String()
}

func (m Model) viewTimeline(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(sessionHeader(s) + "\n\n")

	filtered := m.filteredTimeline()
	if len(filtered) == 0 {
		if len(m.timelineEntries) == 0 {
			return b.String() + mutedStyle.Render("  No events recorded")
		}
		return b.String() + mutedStyle.Render(fmt.Sprintf("  No events match %q", m.filterText))
	}

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		e := filtered[i]
		timeStr := e.time.Format("15:04:05")
		icon := "── "
		switch e.status {
		case "fail":
			icon = statusFail.String() + " "
		case "success", "ok", "end":
			icon = statusOk.String() + " "
		case "start":
			icon = statusActive.String() + " "
		}

		detail := displayTruncate(e.detail, width-16)
		line := fmt.Sprintf("  %s %s %s", mutedStyle.Render(timeStr), icon, detail)
		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more", len(filtered)-end)) + "\n")
	}

	return b.String()
}

// platformBadge returns a short colored tag: "CC" for Claude, "CX" for Codex.
func platformBadge(platform string) string {
	switch platform {
	case "codex":
		return lipgloss.NewStyle().Foreground(colorSuccess).Bold(true).Render("CX")
	default:
		return lipgloss.NewStyle().Foreground(colorSecondary).Render("CC")
	}
}

func formatTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatStartTime shows "13:25" for today, "03/20 15:07" for older.
func formatStartTime(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("01/02 15:04")
}

// cacheHitRate returns a formatted cache hit rate string like "Cache: 85%".
func cacheHitRate(s storage.SessionRow) string {
	total := s.TotalCacheReadTokens + s.TotalCacheCreationTokens
	if total == 0 {
		return ""
	}
	pct := float64(s.TotalCacheReadTokens) / float64(total) * 100
	return fmt.Sprintf("Cache: %.0f%%", pct)
}

// sessionHeader builds the "Session: X │ Context: Y │ Cache: Z% │ $N" header.
func sessionHeader(s storage.SessionRow) string {
	shortID := s.SessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	sep := mutedStyle.Render(" │ ")
	parts := []string{
		headerStyle.Render(fmt.Sprintf("  Session: %s", sessionDisplayName(s))) + mutedStyle.Render(" ("+shortID+")"),
		headerStyle.Render("Ctx: ") + contextPercent(s.LatestContextTokens, s.Model),
	}
	if cr := cacheHitRate(s); cr != "" {
		parts = append(parts, headerStyle.Render(cr))
	}
	costText := fmt.Sprintf("$%.2f", s.TotalCostUSD)
	if s.TotalCostUSD < 0.005 {
		costText = "-"
	}
	parts = append(parts, costStyle.Render(costText))
	return strings.Join(parts, sep)
}

// contextColorize returns a color-coded string based on context window usage percent.
func contextColorize(latest int, model, text string) string {
	window := contextWindowForModel(model)
	pct := float64(latest) / float64(window) * 100
	switch {
	case pct >= 80:
		return contextDangerStyle.Render(text)
	case pct >= 50:
		return contextWarnStyle.Render(text)
	default:
		return contextOkStyle.Render(text)
	}
}

// contextPercent formats the context window usage with color coding.
func contextPercent(latest int, model string) string {
	if latest == 0 {
		return mutedStyle.Render("-")
	}
	window := contextWindowForModel(model)
	pct := float64(latest) / float64(window) * 100
	text := fmt.Sprintf("%s (%.0f%%)", formatTokens(latest), pct)
	switch {
	case pct >= 80:
		return contextDangerStyle.Render(text)
	case pct >= 50:
		return contextWarnStyle.Render(text)
	default:
		return contextOkStyle.Render(text)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// displayTruncate truncates a string to fit within maxCols terminal columns.
// CJK characters count as 2 columns, others as 1.
func displayTruncate(s string, maxCols int) string {
	if maxCols < 4 {
		maxCols = 4
	}
	cols := 0
	for i, r := range s {
		w := 1
		if r >= 0x1100 && isWide(r) {
			w = 2
		}
		if cols+w > maxCols-3 { // reserve 3 for "..."
			return s[:i] + "..."
		}
		cols += w
	}
	return s
}

// isWide returns true for characters that take 2 columns in a terminal.
func isWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		(r >= 0x2E80 && r <= 0x9FFF) || // CJK
		(r >= 0xAC00 && r <= 0xD7AF) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compat Ideographs
		(r >= 0xFE10 && r <= 0xFE6F) || // CJK forms
		(r >= 0xFF01 && r <= 0xFF60) || // Fullwidth
		(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth signs
		(r >= 0x20000 && r <= 0x2FFFF) || // CJK Ext B-F
		(r >= 0x30000 && r <= 0x3FFFF) // CJK Ext G+
}

// fmtKey renders a keybinding hint like "Tab view" with the key highlighted.
func fmtKey(k, desc string) string {
	return keyStyle.Render(k) + " " + keyDescStyle.Render(desc)
}

// copyToClipboard copies text to the system clipboard.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("cmd", "/C", "clip")
	default: // linux/bsd
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// claudeHooksConfigured returns true if agmon hooks are present in ~/.claude/settings.json.
func claudeHooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "agmon emit")
}

