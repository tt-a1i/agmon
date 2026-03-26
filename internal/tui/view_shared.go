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

const dashboardBadgeWidth = 5

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
		return claudeBadgeStyle.Render("CC")
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

func claudeHooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "agmon emit")
}
