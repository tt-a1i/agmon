package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tt-a1i/agmon/internal/storage"
)

// EventMsg is a tea.Msg that signals new data is available.
type EventMsg struct{}

type tab int

const (
	tabDashboard tab = iota
	tabAgentTree
	tabToolCalls
	tabTimeline
)

var tabNames = []string{"Dashboard", "Agent Tree", "Tool Calls", "Timeline"}

type Model struct {
	db              *storage.DB
	eventCh         chan EventMsg
	activeTab       tab
	sessions        []storage.SessionRow
	agents          []storage.AgentRow
	toolCalls       []storage.ToolCallRow
	fileChanges     []storage.FileChangeRow
	selectedSession int
	selectedRow     int
	width           int
	height          int
	todayCost       float64
	activeCount     int
	err             error
}

type tickMsg time.Time
type refreshMsg struct{}

func NewModel(db *storage.DB, eventCh chan EventMsg) Model {
	return Model{
		db:      db,
		eventCh: eventCh,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		listenEvents(m.eventCh),
		refreshCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func refreshCmd() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			m.activeTab = (m.activeTab + 1) % 4
			m.selectedRow = 0
			return m, refreshCmd()
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			m.activeTab = (m.activeTab + 3) % 4
			m.selectedRow = 0
			return m, refreshCmd()
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			m.selectedRow++
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.selectedRow > 0 {
				m.selectedRow--
			}
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.activeTab == tabDashboard && len(m.sessions) > 0 {
				m.selectedSession = m.selectedRow
				m.activeTab = tabAgentTree
				m.selectedRow = 0
				return m, refreshCmd()
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
	m.todayCost, _ = m.db.GetTodayCost()
	m.activeCount, _ = m.db.GetActiveSessionCount()

	if len(m.sessions) > 0 {
		if m.selectedSession >= len(m.sessions) {
			m.selectedSession = 0
		}
		sid := m.sessions[m.selectedSession].SessionID
		m.agents, _ = m.db.ListAgents(sid)
		m.toolCalls, _ = m.db.ListToolCalls(sid, 100)
		m.fileChanges, _ = m.db.ListFileChanges(sid)
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	header := titleStyle.Render("⚡ agmon") + mutedStyle.Render("  AI Agent Monitor")
	b.WriteString(header + "\n\n")

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
	contentHeight := m.height - 8
	if contentHeight < 10 {
		contentHeight = 10
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
	b.WriteString(mutedStyle.Render(" Tab: switch view  j/k: navigate  Enter: select  q: quit"))

	return b.String()
}

func (m Model) viewDashboard(width int) string {
	var b strings.Builder

	// Summary line
	summary := fmt.Sprintf("Active: %s  Today: %s",
		headerStyle.Render(fmt.Sprintf("%d sessions", m.activeCount)),
		costStyle.Render(fmt.Sprintf("$%.2f", m.todayCost)),
	)
	b.WriteString(summary + "\n\n")

	// Session table
	if len(m.sessions) == 0 {
		b.WriteString(mutedStyle.Render("  No sessions yet. Start using Claude Code or Codex to see data."))
		return b.String()
	}

	hdr := fmt.Sprintf("  %-20s %-8s %10s %8s  %s", "SESSION", "PLATFORM", "TOKENS", "COST", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	for i, s := range m.sessions {
		tokens := formatTokens(s.TotalInputTokens + s.TotalOutputTokens)
		cost := fmt.Sprintf("$%.2f", s.TotalCostUSD)
		status := statusActive.String()
		if s.Status == "ended" {
			status = statusEnded.String()
		}

		sid := s.SessionID
		if len(sid) > 20 {
			sid = sid[:20]
		}

		line := fmt.Sprintf("  %-20s %-8s %10s %8s  %s",
			sid, s.Platform, tokens, cost, status)

		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Model) viewAgentTree(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(headerStyle.Render(fmt.Sprintf("  Session: %s", s.SessionID)) + "\n\n")

	if len(m.agents) == 0 {
		b.WriteString(mutedStyle.Render("  No agents recorded"))
		return b.String()
	}

	// Build tree
	for _, a := range m.agents {
		prefix := "  "
		if a.ParentAgentID != "" {
			prefix = "    ├─ "
		} else {
			prefix = "  ▼ "
		}

		status := statusActive.String()
		if a.Status == "ended" {
			status = statusOk.String()
		}

		role := a.Role
		if role == "" {
			role = "agent"
		}

		inputTok, outputTok, cost, _ := m.db.GetSessionTokenSummary(a.AgentID)
		tokens := formatTokens(inputTok + outputTok)

		line := fmt.Sprintf("%s%s %s  %s  %s",
			prefix, role, mutedStyle.Render(a.AgentID[:min(8, len(a.AgentID))]),
			tokens, status)
		if cost > 0 {
			line += "  " + costStyle.Render(fmt.Sprintf("$%.2f", cost))
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Model) viewToolCalls(width int) string {
	var b strings.Builder

	if len(m.toolCalls) == 0 {
		return mutedStyle.Render("  No tool calls recorded")
	}

	hdr := fmt.Sprintf("  %-8s %-12s %-30s %8s  %s", "TIME", "TOOL", "TARGET", "DURATION", "STATUS")
	b.WriteString(headerStyle.Render(hdr) + "\n")

	for i, tc := range m.toolCalls {
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
	}

	return b.String()
}

func (m Model) viewTimeline(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	type timelineEntry struct {
		time    time.Time
		kind    string
		detail  string
		status  string
	}

	var entries []timelineEntry

	// Add agent events
	for _, a := range m.agents {
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

	// Add tool calls
	for _, tc := range m.toolCalls {
		entries = append(entries, timelineEntry{
			time:   tc.StartTime,
			kind:   "tool",
			detail: fmt.Sprintf("%s %s", tc.ToolName, truncate(tc.ParamsSummary, 40)),
			status: tc.Status,
		})
	}

	// Add file changes
	for _, fc := range m.fileChanges {
		entries = append(entries, timelineEntry{
			time:   fc.Timestamp,
			kind:   "file",
			detail: fmt.Sprintf("%s %s", fc.ChangeType, fc.FilePath),
			status: "ok",
		})
	}

	// Sort by time (simple bubble sort, entries are usually small)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].time.Before(entries[i].time) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if len(entries) == 0 {
		return mutedStyle.Render("  No events recorded")
	}

	for i, e := range entries {
		timeStr := e.time.Format("15:04:05")
		icon := "──"
		switch e.status {
		case "fail":
			icon = statusFail.String() + " "
		case "success", "ok", "end":
			icon = statusOk.String() + " "
		case "start":
			icon = statusActive.String() + " "
		default:
			icon = "── "
		}

		line := fmt.Sprintf("  %s %s %s", mutedStyle.Render(timeStr), icon, e.detail)
		if i == m.selectedRow {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
