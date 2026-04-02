package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/tt-a1i/agmon/internal/storage"
)


const splashLogo = `
     ██████╗  ██████╗ ███╗   ███╗ ██████╗ ███╗   ██╗
    ██╔══██╗██╔════╝ ████╗ ████║██╔═══██╗████╗  ██║
    ███████║██║  ███╗██╔████╔██║██║   ██║██╔██╗ ██║
    ██╔══██║██║   ██║██║╚██╔╝██║██║   ██║██║╚██╗██║
    ██║  ██║╚██████╔╝██║ ╚═╝ ██║╚██████╔╝██║ ╚████║
    ╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`

const dashboardBadgeWidth = 6

func (m Model) viewSplash() string {
	var b strings.Builder

	padTop := (m.height - 10) / 2
	if padTop < 0 {
		padTop = 0
	}
	for i := 0; i < padTop; i++ {
		b.WriteString("\n")
	}

	lines := strings.Split(splashLogo, "\n")
	for i, line := range lines {
		if i == 0 && line == "" {
			continue
		}
		if m.splashTick >= i {
			b.WriteString(titleStyle.Render(line) + "\n")
		}
	}

	if m.splashTick >= 8 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("          AI Agent Monitor — cost, context, control") + "\n")
	}
	if m.splashTick >= 12 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("                    press any key to start") + "\n")
	}

	return b.String()
}

func platformBadge(platform string) string {
	switch platform {
	case "codex":
		return codexBadgeStyle.Render("Codex")
	default:
		return claudeBadgeStyle.Render("Claude")
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

func formatStartTime(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("01/02 15:04")
}

func cacheHitRate(s storage.SessionRow) string {
	total := s.TotalCacheReadTokens + s.TotalCacheCreationTokens
	if total == 0 {
		return ""
	}
	pct := float64(s.TotalCacheReadTokens) / float64(total) * 100
	return fmt.Sprintf("Cache: %.0f%%", pct)
}

func dashboardStatus(status string) string {
	switch status {
	case "ended":
		return mutedStyle.Render("end")
	case "stale":
		return mutedStyle.Render("---")
	default:
		return lipgloss.NewStyle().Foreground(colorSuccess).Render("● run")
	}
}

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
		if cols+w > maxCols-3 {
			return s[:i] + "..."
		}
		cols += w
	}
	return s
}

func isWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0x9FFF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE6F) ||
		(r >= 0xFF01 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x2FFFF) ||
		(r >= 0x30000 && r <= 0x3FFFF)
}

func fmtKey(k, desc string) string {
	return keyStyle.Render(k) + " " + keyDescStyle.Render(desc)
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("cmd", "/C", "clip")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// truncateTag truncates a tag to 30 visible characters (rune-aware).
func truncateTag(tag string) string {
	runes := []rune(tag)
	if len(runes) > 30 {
		return string(runes[:27]) + "..."
	}
	return tag
}

// renderCostChart renders a vertical bar chart of daily costs.
// Returns a slice of lines ready for display.
func renderCostChart(daily []storage.DailyCost, width int) []string {
	if len(daily) == 0 {
		return []string{mutedStyle.Render("  No cost data")}
	}

	// Find total and max for scaling.
	maxCost := 0.0
	totalCost := 0.0
	for _, d := range daily {
		totalCost += d.Cost
		if d.Cost > maxCost {
			maxCost = d.Cost
		}
	}

	const chartHeight = 8
	const colWidth = 8   // chars per bar column
	const labelWidth = 8 // left axis label width

	var lines []string
	// Title line
	totalStr := fmt.Sprintf("$%.2f", totalCost)
	lines = append(lines, fmt.Sprintf("  %s    %s %s",
		headerStyle.Render("7-Day Cost"),
		mutedStyle.Render("Total:"), costStyle.Render(totalStr)))
	lines = append(lines, "")

	if maxCost < 0.001 {
		lines = append(lines, mutedStyle.Render("  No cost in the past 7 days"))
		return lines
	}

	// Build the chart rows from top to bottom.
	for row := chartHeight; row >= 1; row-- {
		threshold := maxCost * float64(row) / float64(chartHeight)

		// Y-axis label (only on top, middle, bottom)
		label := strings.Repeat(" ", labelWidth)
		switch row {
		case chartHeight:
			label = fmt.Sprintf("%*s", labelWidth, formatCost(maxCost))
		case chartHeight / 2:
			label = fmt.Sprintf("%*s", labelWidth, formatCost(maxCost/2))
		case 1:
			label = fmt.Sprintf("%*s", labelWidth, formatCost(0))
		}

		var rowBuf strings.Builder
		rowBuf.WriteString(mutedStyle.Render(label) + " ")

		for _, d := range daily {
			if d.Cost >= threshold {
				rowBuf.WriteString(costStyle.Render("  ███  ") + " ")
			} else {
				rowBuf.WriteString(strings.Repeat(" ", colWidth))
			}
		}
		lines = append(lines, rowBuf.String())
	}

	// X-axis line
	var axisBuf strings.Builder
	axisBuf.WriteString(mutedStyle.Render(strings.Repeat(" ", labelWidth) + " "))
	for range daily {
		axisBuf.WriteString(mutedStyle.Render("────────"))
	}
	lines = append(lines, axisBuf.String())

	// Date labels
	var dateBuf strings.Builder
	dateBuf.WriteString(strings.Repeat(" ", labelWidth) + " ")
	for _, d := range daily {
		day := d.Date
		if len(day) >= 10 {
			day = day[5:10]
			day = strings.ReplaceAll(day, "-", "/")
		}
		dateBuf.WriteString(mutedStyle.Render(fmt.Sprintf(" %-7s", day)))
	}
	lines = append(lines, dateBuf.String())

	// Cost values below each bar
	var valBuf strings.Builder
	valBuf.WriteString(strings.Repeat(" ", labelWidth) + " ")
	for _, d := range daily {
		if d.Cost < 0.005 {
			valBuf.WriteString(mutedStyle.Render(fmt.Sprintf(" %-7s", "-")))
		} else {
			valBuf.WriteString(costStyle.Render(fmt.Sprintf(" %-7s", formatCost(d.Cost))))
		}
	}
	lines = append(lines, valBuf.String())

	return lines
}

// formatCost formats a cost value compactly for chart labels.
func formatCost(c float64) string {
	switch {
	case c >= 1000:
		return fmt.Sprintf("$%.0f", c)
	case c >= 100:
		return fmt.Sprintf("$%.0f", c)
	case c >= 10:
		return fmt.Sprintf("$%.1f", c)
	case c >= 1:
		return fmt.Sprintf("$%.2f", c)
	case c >= 0.01:
		return fmt.Sprintf("$%.2f", c)
	default:
		return "$0"
	}
}

func claudeHooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "agmon emit")
}
