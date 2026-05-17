package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func (m *Model) startGlobalSearch() {
	m.searchMode = true
	m.searchText = ""
	m.searchErr = ""
	m.searchHits = nil
	m.searchSelected = 0
}

func (m *Model) closeGlobalSearch() {
	m.searchMode = false
	m.searchText = ""
	m.searchErr = ""
	m.searchHits = nil
	m.searchSelected = 0
}

func (m Model) searchPopupOpen() bool {
	return m.searchErr != "" || m.searchHits != nil
}

func (m *Model) updateSearchInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return *m, tea.Quit
	case tea.KeyEsc:
		m.closeGlobalSearch()
		return *m, nil
	case tea.KeyEnter:
		m.runGlobalSearch()
		return *m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(m.searchText)
		if len(runes) > 0 {
			m.searchText = string(runes[:len(runes)-1])
		}
		return *m, nil
	case tea.KeyRunes:
		m.searchText += string(msg.Runes)
		return *m, nil
	}
	return *m, nil
}

func (m *Model) runGlobalSearch() {
	query := strings.TrimSpace(m.searchText)
	m.searchMode = false
	m.searchSelected = 0
	m.searchErr = ""
	if len([]rune(query)) < 2 {
		m.searchErr = "Search needs at least 2 characters"
		m.searchHits = []storage.SearchHit{}
		return
	}

	hits, err := m.db.SearchHits(query, 20)
	if err != nil {
		m.searchErr = "Search unavailable"
		m.searchHits = []storage.SearchHit{}
		return
	}
	m.searchText = query
	m.searchHits = hits
}

func (m *Model) updateSearchPopup(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.searchPopupOpen() {
		return false, nil
	}
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.closeGlobalSearch()
		return true, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
		if m.searchSelected < len(m.searchHits)-1 {
			m.searchSelected++
		}
		return true, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		if m.searchSelected > 0 {
			m.searchSelected--
		}
		return true, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if len(m.searchHits) == 0 {
			return true, nil
		}
		return true, m.openSelectedSearchHit()
	}
	return false, nil
}

func (m *Model) openSelectedSearchHit() tea.Cmd {
	if m.searchSelected < 0 || m.searchSelected >= len(m.searchHits) {
		return nil
	}
	hit := m.searchHits[m.searchSelected]
	for i, s := range m.sessions {
		if s.SessionID == hit.SessionID {
			m.selectedSession = i
			m.activeTab = tabMessages
			m.resetListPosition()
			m.resetFilter()
			m.closeGlobalSearch()
			return refreshCmd()
		}
	}

	s, found, err := m.db.GetSessionByIDPrefix(hit.SessionID)
	if err != nil || !found {
		m.searchErr = "Session not found"
		m.searchHits = []storage.SearchHit{}
		m.searchSelected = 0
		return nil
	}
	m.sessions = append([]storage.SessionRow{s}, m.sessions...)
	m.selectedSession = 0
	m.activeTab = tabMessages
	m.resetListPosition()
	m.resetFilter()
	m.closeGlobalSearch()
	return refreshCmd()
}

func (m Model) viewSearchPopup(width int) string {
	if !m.searchPopupOpen() {
		return ""
	}

	var b strings.Builder
	title := "Global Search"
	if m.searchText != "" {
		title = fmt.Sprintf("Global Search: %q", m.searchText)
	}
	b.WriteString(headerStyle.Render("  " + title))
	b.WriteString("\n")

	if m.searchErr != "" {
		b.WriteString("  " + errorStyle.Render(m.searchErr))
		return b.String()
	}
	if len(m.searchHits) == 0 {
		b.WriteString("  " + mutedStyle.Render("No matches across tool calls / file changes."))
		return b.String()
	}

	limit := len(m.searchHits)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		hit := m.searchHits[i]
		line := fmt.Sprintf("  %s %s  %s  %s",
			searchKindIcon(hit.Kind),
			headerStyle.Render(displayTruncate(hit.SessionName, 22)),
			displayTruncate(strings.ReplaceAll(hit.Excerpt, "\n", " "), width-44),
			mutedStyle.Render(hit.Timestamp.Format("01/02 15:04")))
		if i == m.searchSelected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(m.searchHits) > limit {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more", len(m.searchHits)-limit)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func searchKindIcon(kind string) string {
	switch kind {
	case "tool_result":
		return "📤"
	case "file":
		return "📁"
	default:
		return "🔧"
	}
}
