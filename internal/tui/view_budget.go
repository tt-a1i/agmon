package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderBudgetChips() string {
	if len(m.budgetChips) == 0 {
		return ""
	}
	chips := make([]string, 0, len(m.budgetChips))
	for _, chip := range m.budgetChips {
		dot := budgetStatusDot(chip.Status)
		text := fmt.Sprintf("[%s: %s/%s (%.0f%%) %s]",
			chip.Name,
			formatBudgetAmount(chip.Used),
			formatBudgetAmount(chip.Limit),
			chip.Percent,
			dot)
		chips = append(chips, text)
	}
	return "  " + strings.Join(chips, "  ")
}

func (m Model) renderTagChips() string {
	if len(m.sessions) == 0 {
		return ""
	}
	options := dashboardTagFilterOptions(m.sessions)
	if len(options) <= 1 {
		return ""
	}
	chips := make([]string, 0, len(options))
	for _, option := range options {
		label := tagFilterDisplayName(option)
		text := "[" + label + "]"
		if option == m.tagFilter {
			text = keyStyle.Render(text)
		} else {
			text = mutedStyle.Render(text)
		}
		chips = append(chips, text)
	}
	return "  Tags: " + strings.Join(chips, " ")
}

func tagFilterDisplayName(filter string) string {
	switch filter {
	case tagFilterAll:
		return "all"
	case tagFilterUntagged:
		return "untagged"
	default:
		return filter
	}
}

func budgetStatusDot(status string) string {
	switch status {
	case budgetStatusOver:
		return lipgloss.NewStyle().Foreground(colorError).Render("◯")
	case budgetStatusWarn:
		return lipgloss.NewStyle().Foreground(colorWarning).Render("◐")
	default:
		return lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
	}
}

func formatBudgetAmount(v float64) string {
	if math.Abs(v-math.Round(v)) < 0.005 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
