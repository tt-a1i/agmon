package report

import (
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestSessionShareMarkdownIncludesGrowthSummary(t *testing.T) {
	start := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	session := storage.SessionRow{
		SessionID:         "session-abcdef123456789",
		Platform:          "codex",
		StartTime:         start,
		EndTime:           &end,
		TotalInputTokens:  12500,
		TotalOutputTokens: 3400,
		TotalCostUSD:      1.25,
		CWD:               "/Users/admin/code/agmon",
		GitBranch:         "main",
		Model:             "gpt-5.5",
		Tag:               "release polish",
	}
	tools := []storage.ToolStatRow{
		{ToolName: "Edit", Count: 3, AvgMs: 1200},
		{ToolName: "Bash", Count: 5, AvgMs: 800, FailCount: 1},
	}
	files := []storage.FileChangeRow{
		{FilePath: "README.md", ChangeType: "edit"},
		{FilePath: "internal/report/session.go", ChangeType: "create"},
	}

	got := SessionShareMarkdown(session, tools, files, end)

	for _, want := range []string{
		"# TokenMeter Session: agmon/main",
		"- Platform: codex",
		"- Model: gpt-5.5",
		"- Cost: $1.25",
		"- Tokens: 12.5k in / 3.4k out / 15.9k total",
		"- Duration: 1h30m0s",
		"- Tag: release polish",
		"## Top Tools",
		"- Bash: 5 calls, avg 800ms, 1 failed",
		"## File Changes",
		"- 2 files touched (1 created, 1 edited, 0 deleted)",
		"- + internal/report/session.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("share markdown missing %q:\n%s", want, got)
		}
	}
}
