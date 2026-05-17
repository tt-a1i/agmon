package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestShortSessionIDForDisplay(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"abc", "abc"},                      // short stays unchanged
		{"12345678", "12345678"},            // exact 8 stays
		{"abc-def-ghi-jkl-mno", "abc-def-"}, // long truncates to 8
		{"", ""},                            // empty
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := shortSessionIDForDisplay(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestTruncateTag(t *testing.T) {
	short := "small tag"
	if got := truncateTag(short); got != short {
		t.Errorf("short tag mutated: %q", got)
	}

	long := strings.Repeat("a", 50)
	got := truncateTag(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("long tag should have ellipsis, got %q", got)
	}
	if len([]rune(got)) != 30 {
		t.Errorf("rune length = %d, want 30", len([]rune(got)))
	}

	// Multi-byte: 30 runes of 中 should not be truncated; 31 should be.
	exact30 := strings.Repeat("中", 30)
	if got := truncateTag(exact30); got != exact30 {
		t.Errorf("30 runes mutated: %q", got)
	}
	beyond := strings.Repeat("中", 35)
	got = truncateTag(beyond)
	// Result should be 27 runes + "..." = 30 visible chars
	r := []rune(got)
	if len(r) != 30 {
		t.Errorf("over-30 runes truncated to %d, want 30", len(r))
	}
}

func TestRenderTrendBoundaries(t *testing.T) {
	// prev == 0 returns empty string
	if got := renderTrend(5, 0); got != "" {
		t.Errorf("prev=0 should return empty, got %q", got)
	}
	// Increase >10% returns up arrow + percent
	if got := renderTrend(2, 1); !strings.Contains(got, "↑") {
		t.Errorf("increase should show ↑, got %q", got)
	}
	// Decrease >10% returns down arrow
	if got := renderTrend(0.5, 1); !strings.Contains(got, "↓") {
		t.Errorf("decrease should show ↓, got %q", got)
	}
	// Within ±10% returns horizontal arrow
	if got := renderTrend(1.05, 1); !strings.Contains(got, "→") {
		t.Errorf("flat should show →, got %q", got)
	}
}

func TestFormatCostScales(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.001, "$0"},
		{0.50, "$0.50"},
		{5.0, "$5.00"},
		{50.0, "$50.0"},
		{500.0, "$500"},
		{5000.0, "$5000"},
	}
	for _, c := range cases {
		got := formatCost(c.in)
		if got != c.want {
			t.Errorf("formatCost(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStartOfCalendarWeek(t *testing.T) {
	// 2026-01-14 is a Wednesday; start of its calendar week is Monday 2026-01-12.
	wed := time.Date(2026, 1, 14, 15, 30, 0, 0, time.Local)
	monday := startOfCalendarWeek(wed)
	if monday.Weekday() != time.Monday {
		t.Errorf("week start weekday = %v, want Monday", monday.Weekday())
	}
	if monday.Day() != 12 || monday.Month() != 1 || monday.Year() != 2026 {
		t.Errorf("week start = %v, want 2026-01-12", monday)
	}

	// Sunday is rolled to the previous Monday (week starts Monday).
	sun := time.Date(2026, 1, 18, 12, 0, 0, 0, time.Local)
	prevMon := startOfCalendarWeek(sun)
	if prevMon.Day() != 12 || prevMon.Month() != 1 {
		t.Errorf("sunday's week-start = %v, want 2026-01-12", prevMon)
	}
}

func TestStartOfCalendarMonth(t *testing.T) {
	mid := time.Date(2026, 4, 17, 10, 0, 0, 0, time.Local)
	got := startOfCalendarMonth(mid)
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 1 {
		t.Errorf("month start = %v, want 2026-04-01", got)
	}
	if got.Hour() != 0 || got.Minute() != 0 {
		t.Errorf("month start should be 00:00, got %v", got)
	}
}

// TestRestoreTabCursorResetsOnNonDashboard covers the simple state-machine
// behavior: leaving the dashboard tab resets cursor + viewOffset to 0.
func TestRestoreTabCursorResetsOnNonDashboard(t *testing.T) {
	db, _ := storage.Open(t.TempDir() + "/c.db")
	t.Cleanup(func() { db.Close() })
	m := NewModel(db, nil)
	m.activeTab = tabToolCalls
	m.selectedRow = 7
	m.viewOffset = 3
	m.filterText = "junk"

	m.restoreTabCursor()

	if m.selectedRow != 0 || m.viewOffset != 0 {
		t.Errorf("expected reset to 0,0; got row=%d off=%d", m.selectedRow, m.viewOffset)
	}
	if m.filterText != "" {
		t.Errorf("filter should be cleared, got %q", m.filterText)
	}
}

// TestRestoreTabCursorOnDashboardSyncsRow covers the dashboard branch that
// tries to put the cursor back on the selected session. With no sessions
// loaded it falls through to the 0,0 default.
func TestRestoreTabCursorOnDashboardWithEmptyData(t *testing.T) {
	db, _ := storage.Open(t.TempDir() + "/c2.db")
	t.Cleanup(func() { db.Close() })
	m := NewModel(db, nil)
	m.activeTab = tabDashboard
	m.selectedSession = 5 // beyond len(sessions) = 0
	m.selectedRow = 9
	m.viewOffset = 4

	m.restoreTabCursor()

	if m.selectedRow != 0 || m.viewOffset != 0 {
		t.Errorf("dashboard with empty data should default to 0,0; got row=%d off=%d", m.selectedRow, m.viewOffset)
	}
}

func TestClaudeHooksConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No settings.json → false
	if got := claudeHooksConfigured(); got {
		t.Errorf("no settings.json should report false")
	}

	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Settings without the hook → false
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"),
		[]byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := claudeHooksConfigured(); got {
		t.Errorf("settings without hook should report false")
	}

	// Settings WITH "tokenmeter emit" → true
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"),
		[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"tokenmeter emit"}]}]}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := claudeHooksConfigured(); !got {
		t.Errorf("settings with hook should report true")
	}
}
