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
	if breakdown := m.renderModelBreakdown(width); breakdown != "" {
		b.WriteString(breakdown + "\n")
	}

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
		expanded := m.expandedCalls[messageExpansionKeyForFiltered(m.messages, filtered, i)]

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
				// Wrap long lines by chunks that fit width-10. displayTruncate
				// returns at a rune boundary, so byte slicing the chunk's
				// "...-stripped" prefix is safe (no half rune).
				remaining := rawLine
				for len(remaining) > 0 {
					chunk := displayTruncate(remaining, width-10)
					// Content equality (not length): when displayTruncate cuts at
					// i == len(s)-3 the result `s[:i] + "..."` has the SAME length
					// as the input but isn't `==` it. Length-only comparison would
					// drop the trailing 3 bytes of user content on this boundary.
					if chunk == remaining {
						b.WriteString(mutedStyle.Render("         "+chunk) + "\n")
						break
					}
					// displayTruncate appended "..." — strip those 3 ASCII bytes
					// to get the rune-aligned visible prefix for continuation.
					visible := chunk[:len(chunk)-3]
					b.WriteString(mutedStyle.Render("         "+visible) + "\n")
					if len(visible) == 0 {
						break // degenerate width: nothing fits
					}
					remaining = remaining[len(visible):]
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
