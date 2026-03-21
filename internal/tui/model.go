package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tt-a1i/agmon/internal/storage"
)

// EventMsg signals new data is available from the daemon.
type EventMsg struct{}

type tab int

const (
	tabDashboard tab = iota
	tabAgentTree
	tabToolCalls
	tabTimeline
)

var tabNames = []string{"Dashboard", "Agent Tree", "Tool Calls", "Timeline"}

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
	activeTab       tab
	sessions        []storage.SessionRow
	agents          []storage.AgentRow
	toolCalls       []storage.ToolCallRow
	fileChanges     []storage.FileChangeRow
	timelineEntries []timelineEntry
	selectedSession int
	selectedRow     int
	viewOffset      int
	expandedCalls   map[string]bool // call_id -> expanded
	filterMode      bool
	filterText      string
	todayInput      int
	todayOutput     int
	todayCost       float64
	width           int
	height          int
	activeCount     int
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

func (m Model) filteredAgents() []storage.AgentRow {
	if m.filterText == "" {
		return m.agents
	}
	f := strings.ToLower(m.filterText)
	var out []storage.AgentRow
	for _, a := range m.agents {
		if strings.Contains(strings.ToLower(a.Role), f) ||
			strings.Contains(strings.ToLower(a.AgentID), f) {
			out = append(out, a)
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
	case tabAgentTree:
		return len(m.filteredAgents())
	case tabToolCalls:
		return len(m.filteredToolCalls())
	case tabTimeline:
		return len(m.filteredTimeline())
	}
	return 0
}

// adjustScroll keeps selectedRow visible within the viewport.
func (m *Model) adjustScroll() {
	visible := m.visibleRows()
	if m.selectedRow < m.viewOffset {
		m.viewOffset = m.selectedRow
	} else if m.selectedRow >= m.viewOffset+visible {
		m.viewOffset = m.selectedRow - visible + 1
	}
	if m.viewOffset < 0 {
		m.viewOffset = 0
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.activeTab = (m.activeTab + 1) % 4
			m.selectedRow = 0
			m.viewOffset = 0
			return m, refreshCmd()

		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			m.activeTab = (m.activeTab + 3) % 4
			m.selectedRow = 0
			m.viewOffset = 0
			return m, refreshCmd()

		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if count := m.currentTabRowCount(); count > 0 && m.selectedRow < count-1 {
				m.selectedRow++
				m.adjustScroll()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.selectedRow > 0 {
				m.selectedRow--
				m.adjustScroll()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("["))):
			if m.selectedSession > 0 {
				m.selectedSession--
				m.selectedRow = 0
				m.viewOffset = 0
				return m, refreshCmd()
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("]"))):
			if m.selectedSession < len(m.sessions)-1 {
				m.selectedSession++
				m.selectedRow = 0
				m.viewOffset = 0
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
					// Go to Tool Calls — the most immediately useful view.
					m.activeTab = tabToolCalls
					m.selectedRow = 0
					m.viewOffset = 0
					return m, refreshCmd()
				}
			}
			if m.activeTab == tabToolCalls {
				filtered := m.filteredToolCalls()
				if m.selectedRow < len(filtered) {
					tc := filtered[m.selectedRow]
					m.expandedCalls[tc.CallID] = !m.expandedCalls[tc.CallID]
				}
				return m, nil
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
	m.todayInput, m.todayOutput, _ = m.db.GetTodayTokens()
	m.todayCost, _ = m.db.GetTodayCost()

	if len(m.sessions) > 0 {
		if m.selectedSession >= len(m.sessions) {
			m.selectedSession = 0
		}
		sid := m.sessions[m.selectedSession].SessionID
		m.agents, _ = m.db.ListAgents(sid)
		m.toolCalls, _ = m.db.ListToolCalls(sid, 500)
		m.fileChanges, _ = m.db.ListFileChanges(sid)
		m.timelineEntries = buildTimeline(m.agents, m.toolCalls, m.fileChanges)
	}

	// Clamp selectedRow to prevent stale-index panics.
	if count := m.currentTabRowCount(); m.selectedRow >= count {
		if count > 0 {
			m.selectedRow = count - 1
		} else {
			m.selectedRow = 0
		}
		m.adjustScroll()
	}
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

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("⚡ agmon") + mutedStyle.Render("  AI Agent Monitor") + "\n\n")

	// Tabs
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render(name))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(name))
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n\n")

	// Content
	contentWidth := m.width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	var content string
	switch m.activeTab {
	case tabDashboard:
		content = m.viewDashboard(contentWidth)
	case tabAgentTree:
		content = m.viewAgentTree(contentWidth)
	case tabToolCalls:
		content = m.viewToolCalls(contentWidth)
	case tabTimeline:
		content = m.viewTimeline(contentWidth)
	}

	b.WriteString(boxStyle.Width(contentWidth).Render(content))

	// Footer
	b.WriteString("\n")
	var footer string
	if m.filterMode {
		footer = fmt.Sprintf(" Filter: %s█  Esc: cancel  Enter: confirm", m.filterText)
	} else if m.filterText != "" {
		footer = fmt.Sprintf(" Filter: %s  Esc: clear  Tab: view  j/k: nav  [/]: session  q: quit",
			headerStyle.Render(m.filterText))
	} else {
		footer = " /: filter  Tab: view  j/k: nav  [/]: session  Enter: select  q: quit"
	}
	if m.err != nil {
		footer += "  " + errorStyle.Render("err: "+m.err.Error())
	}
	b.WriteString(mutedStyle.Render(footer))

	return b.String()
}

func (m Model) viewDashboard(width int) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Active: %s  Today: %s in / %s out  Cost: %s\n\n",
		headerStyle.Render(fmt.Sprintf("%d sessions", m.activeCount)),
		mutedStyle.Render(formatTokens(m.todayInput)),
		mutedStyle.Render(formatTokens(m.todayOutput)),
		costStyle.Render(fmt.Sprintf("$%.4f", m.todayCost)),
	))

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

	hdr := fmt.Sprintf("  %-20s %-14s %8s  %7s %8s  %s", "SESSION", "STARTED", "COST", "CTX", "OUT", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	visible := m.visibleRows() - 4
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		s := filtered[i]
		status := statusActive.String()
		switch s.Status {
		case "ended":
			status = statusEnded.String()
		case "stale":
			status = mutedStyle.Render("?")
		}

		name := sessionDisplayName(s)
		if len(name) > 20 {
			name = name[:20]
		}

		started := formatStartTime(s.StartTime)
		costStr := fmt.Sprintf("$%.2f", s.TotalCostUSD)
		ctxStr := formatTokens(s.LatestContextTokens)
		line := fmt.Sprintf("  %-20s %-14s %8s  %7s %8s  %s",
			name, started, costStr, ctxStr,
			formatTokens(s.TotalOutputTokens),
			status)

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more (j to scroll)", len(filtered)-end)) + "\n")
	}

	return b.String()
}

func (m Model) viewAgentTree(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(sessionHeader(s) + "\n\n")

	filtered := m.filteredAgents()
	if len(filtered) == 0 {
		if len(m.agents) == 0 {
			b.WriteString(mutedStyle.Render("  No agents recorded"))
		} else {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  No agents match %q", m.filterText)))
		}
		return b.String()
	}

	visible := m.visibleRows() - 3
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		a := filtered[i]
		prefix := "  ▼ "
		if a.ParentAgentID != "" {
			prefix = "    ├─ "
		}

		status := statusActive.String()
		if a.Status == "ended" {
			status = statusOk.String()
		}

		role := a.Role
		if role == "" {
			role = "agent"
		}

		idLen := len(a.AgentID)
		if idLen > 8 {
			idLen = 8
		}
		line := fmt.Sprintf("%s%s %s  %s",
			prefix, role, mutedStyle.Render(a.AgentID[:idLen]), status)

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

	visible := m.visibleRows() - 2
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
			status = lipgloss.NewStyle().Foreground(colorWarning).Render("↻")
		}

		target := tc.ParamsSummary
		if len(target) > 30 {
			target = target[:30]
		}
		toolName := tc.ToolName
		if len(toolName) > 12 {
			toolName = toolName[:12]
		}

		line := fmt.Sprintf("  %-8s %-12s %-30s %8s  %s",
			timeStr, toolName, target, dur, status)

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")

		if m.expandedCalls[tc.CallID] {
			if tc.ParamsSummary != "" {
				b.WriteString(mutedStyle.Render("    Params: "+tc.ParamsSummary) + "\n")
			}
			if tc.ResultSummary != "" {
				b.WriteString(mutedStyle.Render("    Result: "+tc.ResultSummary) + "\n")
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

	visible := m.visibleRows()
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

		line := fmt.Sprintf("  %s %s %s", mutedStyle.Render(timeStr), icon, e.detail)
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

// relativeTime formats a time as "2m ago", "3h ago", "2d ago", etc.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
	parts := []string{
		fmt.Sprintf("  Session: %s", sessionDisplayName(s)),
		fmt.Sprintf("Ctx: %s", contextPercent(s.LatestContextTokens, s.Model)),
	}
	if cr := cacheHitRate(s); cr != "" {
		parts = append(parts, cr)
	}
	parts = append(parts, costStyle.Render(fmt.Sprintf("$%.2f", s.TotalCostUSD)))
	return headerStyle.Render(strings.Join(parts, "  │  "))
}

// contextPercent formats the context window usage with color coding.
func contextPercent(latest int, model string) string {
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

// claudeHooksConfigured returns true if agmon hooks are present in ~/.claude/settings.json.
func claudeHooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "agmon emit")
}

