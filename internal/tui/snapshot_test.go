package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

var updateGolden = flag.Bool("update-golden", false, "update golden snapshot files")

// snapshotSetup forces Ascii color profile so View() output has no ANSI codes,
// making snapshots stable across terminals and CI environments.
func snapshotSetup(t *testing.T) {
	t.Helper()
	old := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

// stripAnsi removes ANSI escape sequences from s (safety net; Ascii profile
// should produce no codes, but protects against future color additions).
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renderForSnapshot sets width/height and returns the stripped View() output.
func renderForSnapshot(t *testing.T, m *Model, w, h int) string {
	t.Helper()
	m.width = w
	m.height = h
	return stripAnsi(m.View())
}

// assertGolden reads the golden file and compares. With -update-golden it writes.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "snapshots", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s; run with -update-golden to create: %v", path, err)
	}
	if string(want) != got {
		t.Errorf("snapshot mismatch %s\n--- want ---\n%s\n--- got ---\n%s", name, string(want), got)
	}
}

// fixedTime returns a stable timestamp for test data.
func fixedTime() time.Time {
	return time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC)
}

func TestSnapshot_Dashboard_Empty(t *testing.T) {
	snapshotSetup(t)
	m := Model{
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "dashboard_empty.txt", got)
}

func TestSnapshot_Dashboard_SingleSession(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:         "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:          "claude",
				GitBranch:         "main",
				CWD:               "/code/tokenmeter",
				StartTime:         ts,
				TotalInputTokens:  50000,
				TotalOutputTokens: 12000,
				TotalCostUSD:      0.42,
				Model:             "claude-sonnet-4-6",
				Status:            "ended",
			},
		},
		todayCost:     0.42,
		todayInput:    50000,
		todayOutput:   12000,
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "dashboard_single_session.txt", got)
}

func TestSnapshot_Dashboard_MultiSession(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:         "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:          "claude",
				GitBranch:         "feat-xyz",
				CWD:               "/code/tokenmeter",
				StartTime:         ts,
				TotalInputTokens:  500000,
				TotalOutputTokens: 10400,
				TotalCostUSD:      2.86,
				Model:             "claude-sonnet-4-6",
				Status:            "active",
			},
			{
				SessionID:         "aabbccdd-0002-0002-0002-aabbccdd0002",
				Platform:          "codex",
				GitBranch:         "fix-bug",
				CWD:               "/code/api",
				StartTime:         ts.Add(-30 * time.Minute),
				TotalInputTokens:  80000,
				TotalOutputTokens: 20000,
				TotalCostUSD:      0.80,
				Status:            "ended",
			},
			{
				SessionID:         "aabbccdd-0003-0003-0003-aabbccdd0003",
				Platform:          "claude",
				GitBranch:         "refactor",
				CWD:               "/code/web",
				StartTime:         ts.Add(-2 * time.Hour),
				TotalInputTokens:  200000,
				TotalOutputTokens: 5000,
				TotalCostUSD:      1.20,
				Model:             "claude-opus-4-7",
				Status:            "ended",
			},
		},
		todayCost:     4.86,
		todayInput:    780000,
		todayOutput:   37400,
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "dashboard_multi_session.txt", got)
}

func TestSnapshot_Messages_Empty(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		activeTab: tabMessages,
		sessions: []storage.SessionRow{
			{
				SessionID: "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:  "claude",
				GitBranch: "main",
				CWD:       "/code/tokenmeter",
				StartTime: ts,
				Status:    "active",
			},
		},
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "messages_empty.txt", got)
}

func TestSnapshot_Messages_WithContent(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		activeTab: tabMessages,
		sessions: []storage.SessionRow{
			{
				SessionID: "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:  "claude",
				GitBranch: "main",
				CWD:       "/code/tokenmeter",
				StartTime: ts,
				Status:    "active",
			},
		},
		messages: []collector.UserMessage{
			{Timestamp: ts, Content: "Add snapshot golden tests for the TUI"},
			{Timestamp: ts.Add(time.Minute), Content: "Now update the goldens and verify they pass"},
		},
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "messages_with_content.txt", got)
}

func TestSnapshot_ToolCalls_Empty(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		activeTab: tabToolCalls,
		sessions: []storage.SessionRow{
			{
				SessionID: "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:  "claude",
				GitBranch: "main",
				CWD:       "/code/tokenmeter",
				StartTime: ts,
				Status:    "active",
			},
		},
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "tool_calls_empty.txt", got)
}

func TestSnapshot_ToolCalls_WithList(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	end1 := ts.Add(250 * time.Millisecond)
	end2 := ts.Add(time.Second + 120*time.Millisecond)
	end4 := ts.Add(3*time.Second + 45*time.Millisecond)
	end5 := ts.Add(4*time.Second + 88*time.Millisecond)
	m := Model{
		activeTab: tabToolCalls,
		sessions: []storage.SessionRow{
			{
				SessionID: "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:  "claude",
				GitBranch: "main",
				CWD:       "/code/tokenmeter",
				StartTime: ts,
				Status:    "active",
			},
		},
		toolCalls: []storage.ToolCallRow{
			{CallID: "call-1", ToolName: "Read", ParamsSummary: "internal/tui/model.go", StartTime: ts, EndTime: &end1, DurationMs: 250, Status: "success"},
			{CallID: "call-2", ToolName: "Edit", ParamsSummary: "internal/tui/view.go", StartTime: ts.Add(time.Second), EndTime: &end2, DurationMs: 120, Status: "success"},
			{CallID: "call-3", ToolName: "Bash", ParamsSummary: "go test ./...", StartTime: ts.Add(2 * time.Second), DurationMs: 0, Status: "pending"},
			{CallID: "call-4", ToolName: "Grep", ParamsSummary: "func NewModel", StartTime: ts.Add(3 * time.Second), EndTime: &end4, DurationMs: 45, Status: "success"},
			{CallID: "call-5", ToolName: "Write", ParamsSummary: "snapshot_test.go", StartTime: ts.Add(4 * time.Second), EndTime: &end5, DurationMs: 88, Status: "success"},
		},
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "tool_calls_list.txt", got)
}

func TestSnapshot_ToolCalls_Expanded(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	end1 := ts.Add(250 * time.Millisecond)
	end2 := ts.Add(time.Second + 120*time.Millisecond)
	m := Model{
		activeTab: tabToolCalls,
		sessions: []storage.SessionRow{
			{
				SessionID: "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:  "claude",
				GitBranch: "main",
				CWD:       "/code/tokenmeter",
				StartTime: ts,
				Status:    "active",
			},
		},
		toolCalls: []storage.ToolCallRow{
			{CallID: "call-1", ToolName: "Read", ParamsSummary: "internal/tui/model.go", ResultSummary: "ok (420 lines)", StartTime: ts, EndTime: &end1, DurationMs: 250, Status: "success"},
			{CallID: "call-2", ToolName: "Edit", ParamsSummary: "internal/tui/view.go", ResultSummary: "applied 3 changes", StartTime: ts.Add(time.Second), EndTime: &end2, DurationMs: 120, Status: "success"},
		},
		expandedCalls: map[string]bool{"call-1": true},
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "tool_calls_expanded.txt", got)
}

func TestSnapshot_Stats_Empty(t *testing.T) {
	snapshotSetup(t)
	m := Model{
		activeTab:     tabStats,
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 120, 30)
	assertGolden(t, "stats_empty.txt", got)
}

func TestSnapshot_Dashboard_80x24(t *testing.T) {
	snapshotSetup(t)
	ts := fixedTime()
	m := Model{
		sessions: []storage.SessionRow{
			{
				SessionID:         "aabbccdd-0001-0001-0001-aabbccdd0001",
				Platform:          "claude",
				GitBranch:         "main",
				CWD:               "/code/tokenmeter",
				StartTime:         ts,
				TotalInputTokens:  50000,
				TotalOutputTokens: 12000,
				TotalCostUSD:      0.42,
				Status:            "active",
			},
		},
		todayCost:     0.42,
		todayInput:    50000,
		todayOutput:   12000,
		expandedCalls: make(map[string]bool),
	}
	m.refreshFilteredViews()
	got := renderForSnapshot(t, &m, 80, 24)
	assertGolden(t, "dashboard_80x24.txt", got)
}
