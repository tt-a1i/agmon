package tui

import (
	"fmt"
	"math"
	"strings"
)

func (m Model) renderModelBreakdown(width int) string {
	if len(m.modelBreakdown) <= 1 {
		return ""
	}

	totalCost := 0.0
	for _, row := range m.modelBreakdown {
		totalCost += row.CostUSD
	}
	if totalCost <= 0 {
		return ""
	}

	const barWidth = 12
	var b strings.Builder
	b.WriteString(headerStyle.Render("  By Model:") + "\n")
	for _, row := range m.modelBreakdown {
		pct := row.CostUSD / totalCost * 100
		filled := int(math.Round(pct / 100 * barWidth))
		if filled == 0 && pct > 0 {
			filled = 1
		}
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		model := displayTruncate(row.Model, 24)
		line := fmt.Sprintf("  %-24s %8s in / %-8s out  %7s  %s %.0f%%",
			model,
			formatTokens(row.InputTokens),
			formatTokens(row.OutputTokens),
			fmt.Sprintf("$%.2f", row.CostUSD),
			keyStyle.Render(bar),
			pct)
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
