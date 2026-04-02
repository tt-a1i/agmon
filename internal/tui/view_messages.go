package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewMessages(width int) string {
	var b strings.Builder

	if len(m.sessions) == 0 {
		return mutedStyle.Render("  No sessions")
	}

	s := m.sessions[m.selectedSession]
	b.WriteString(sessionHeader(s) + "\n")

	filtered := m.filteredMessages()
	if m.filterText != "" {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d/%d messages", len(filtered), len(m.messages))) + "\n\n")
	} else {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d messages", len(m.messages))) + "\n\n")
	}

	if len(m.messages) == 0 {
		b.WriteString(mutedStyle.Render("  No user messages found"))
		return b.String()
	}

	if len(filtered) == 0 && m.filterText != "" {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  No messages match %q", m.filterText)))
		return b.String()
	}

	visible := m.tabVisibleRows()
	start := m.viewOffset
	end := start + visible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		msg := filtered[i]
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

			for _, rawLine := range strings.Split(msg.Content, "\n") {
				rawLine = strings.TrimSpace(rawLine)
				if rawLine == "" {
					continue
				}
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
			continue
		}

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

	if end < len(filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ... %d more (j to scroll)", len(filtered)-end)) + "\n")
	}

	return b.String()
}
