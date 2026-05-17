package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func seedCLISession(t *testing.T, db *storage.DB, id string, platform event.Platform, cwd, branch string, ts time.Time, inTokens, outTokens int, cost float64) {
	t.Helper()
	if err := db.UpsertSession(id, platform, ts); err != nil {
		t.Fatalf("upsert session %s: %v", id, err)
	}
	if err := db.UpdateSessionMeta(id, cwd, branch); err != nil {
		t.Fatalf("update meta %s: %v", id, err)
	}
	if err := db.UpsertAgent("agent-"+id, id, "", "main", ts); err != nil {
		t.Fatalf("upsert agent %s: %v", id, err)
	}
	if err := db.InsertTokenUsage("agent-"+id, id, inTokens, outTokens, 7, 11, "sonnet", cost, ts, "tok-"+id+"-"+ts.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert token usage %s: %v", id, err)
	}
}

func TestRunExportProducesCSV(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "export-csv", event.PlatformClaude, "/tmp/agmon", "main", now, 120, 34, 1.25)

	withArgs(t, []string{"tokenmeter", "export", "--range", "all", "--format", "csv"})
	out := captureStdout(t, func() {
		if err := runExport(); err != nil {
			t.Fatalf("runExport: %v", err)
		}
	})

	if !strings.HasPrefix(out, "date,session_id,session_name,platform,model,input_tokens,output_tokens,cache_tokens,cost_usd\n") {
		t.Fatalf("csv header missing:\n%s", out)
	}
	for _, want := range []string{"export-csv", "main", "claude", "sonnet", "120", "34", "18", "1.250000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("csv output missing %q:\n%s", want, out)
		}
	}
}

func TestRunExportProducesJSON(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "export-json", event.PlatformCodex, "/tmp/agmon", "feature", now, 500, 60, 2.75)

	withArgs(t, []string{"tokenmeter", "export", "--range", "all", "--format", "json"})
	out := captureStdout(t, func() {
		if err := runExport(); err != nil {
			t.Fatalf("runExport: %v", err)
		}
	})

	var rows []storage.SessionExportRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal export json: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 export row, got %#v", rows)
	}
	if rows[0].SessionID != "export-json" || rows[0].SessionName != "feature" || rows[0].CostUSD != 2.75 {
		t.Fatalf("unexpected export row: %#v", rows[0])
	}
}

func TestRunExportRangeBoundary(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, time.Local)
	if today.After(now) {
		today = now.Add(-time.Minute)
	}
	yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 10, 0, 0, 0, time.Local)
	seedCLISession(t, db, "today-session", event.PlatformClaude, "/tmp/today", "", today, 100, 10, 0.5)
	seedCLISession(t, db, "yesterday-session", event.PlatformClaude, "/tmp/yesterday", "", yesterday, 200, 20, 0.8)

	withArgs(t, []string{"tokenmeter", "export", "--range", "today", "--format", "csv"})
	out := captureStdout(t, func() {
		if err := runExport(); err != nil {
			t.Fatalf("runExport: %v", err)
		}
	})

	if !strings.Contains(out, "today-session") {
		t.Fatalf("today export missing current row:\n%s", out)
	}
	if strings.Contains(out, "yesterday-session") {
		t.Fatalf("today export included yesterday row:\n%s", out)
	}
}

func TestRunCompareTextFormat(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Hour)
	seedCLISession(t, db, "compare-a", event.PlatformClaude, "/tmp/agmon", "main", now, 1000, 200, 42.50)
	seedCLISession(t, db, "compare-b", event.PlatformCodex, "/tmp/agmon", "feature", now.Add(time.Minute), 1500, 500, 67.20)
	for i := 0; i < 2; i++ {
		if _, err := db.InsertToolCallStart("a-edit-"+string(rune('0'+i)), "agent-compare-a", "compare-a", "Edit", "{}", now); err != nil {
			t.Fatalf("insert a edit: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := db.InsertToolCallStart("b-read-"+string(rune('0'+i)), "agent-compare-b", "compare-b", "Read", "{}", now); err != nil {
			t.Fatalf("insert b read: %v", err)
		}
	}
	if err := db.InsertFileChange("compare-a", "a.go", event.FileEdit, now); err != nil {
		t.Fatalf("insert a file: %v", err)
	}
	for _, path := range []string{"a.go", "b.go"} {
		if err := db.InsertFileChange("compare-b", path, event.FileEdit, now); err != nil {
			t.Fatalf("insert b file: %v", err)
		}
	}

	withArgs(t, []string{"tokenmeter", "compare", "compare-a", "compare-b"})
	out := captureStdout(t, func() {
		if err := runCompare(); err != nil {
			t.Fatalf("runCompare: %v", err)
		}
	})

	for _, want := range []string{
		"Session A: claude/main · $42.50",
		"Session B: codex/feature · $67.20",
		"Cost delta:    +$24.70 (+58.1%)",
		"Tool delta:    +1 calls",
		"File delta:    +1 files",
		"- Edit:  A=2 B=0 (-2)",
		"- Read:  A=0 B=3 (+3)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compare text missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompareAmbiguousReturnsError(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "ambiguous-one", event.PlatformClaude, "/tmp/one", "", now, 1, 1, 0.1)
	seedCLISession(t, db, "ambiguous-two", event.PlatformClaude, "/tmp/two", "", now, 1, 1, 0.1)
	seedCLISession(t, db, "other-session", event.PlatformCodex, "/tmp/other", "", now, 1, 1, 0.1)

	withArgs(t, []string{"tokenmeter", "compare", "ambiguous", "other-session"})
	err := runCompare()
	if err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if !strings.Contains(err.Error(), "ambiguous session prefix") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSearchOutputsHits(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "search-cli", event.PlatformClaude, "/tmp/agmon", "main", now, 1, 1, 0.01)
	if _, err := db.InsertToolCallStart("search-param", "agent-search-cli", "search-cli", "Edit", `{"file_path":"needle.go"}`, now); err != nil {
		t.Fatalf("insert tool param: %v", err)
	}
	if err := db.UpdateToolCallEnd("search-param", "output: matched needle text", event.StatusSuccess, 0, now.Add(time.Second)); err != nil {
		t.Fatalf("update tool result: %v", err)
	}
	if err := db.InsertFileChange("search-cli", "src/needle.go", event.FileEdit, now.Add(2*time.Second)); err != nil {
		t.Fatalf("insert file hit: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "search", "needle", "--limit", "5"})
	out := captureStdout(t, func() {
		if err := runSearch(); err != nil {
			t.Fatalf("runSearch: %v", err)
		}
	})

	for _, want := range []string{
		"Found 3 matches:",
		"[tool_param] main",
		`Edit {"file_path":"needle.go"}`,
		"[tool_result] main",
		"output: matched needle text",
		"[file] main",
		"src/needle.go",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("search output missing %q:\n%s", want, out)
		}
	}
}

func TestRunBudgetListSetDelete(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "budget-session", event.PlatformClaude, "/tmp/budget", "", now, 100, 10, 25)

	withArgs(t, []string{"tokenmeter", "budget", "set", "Claude monthly", "100", "--platform", "claude"})
	setOut := captureStdout(t, func() {
		if err := runBudget(); err != nil {
			t.Fatalf("runBudget set: %v", err)
		}
	})
	if !strings.Contains(setOut, "Set budget") {
		t.Fatalf("unexpected set output: %s", setOut)
	}

	withArgs(t, []string{"tokenmeter", "budget", "list"})
	listOut := captureStdout(t, func() {
		if err := runBudget(); err != nil {
			t.Fatalf("runBudget list: %v", err)
		}
	})
	for _, want := range []string{"ID", "NAME", "PLATFORM", "LIMIT", "USED", "PERCENT", "STATUS", "Claude monthly", "claude", "$100.00", "$25.00", "25.0%", "ok"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("budget list missing %q:\n%s", want, listOut)
		}
	}

	budgets, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("expected 1 budget, got %#v", budgets)
	}
	id := budgets[0].ID

	withArgs(t, []string{"tokenmeter", "budget", "usage", "1"})
	usageOut := captureStdout(t, func() {
		if err := runBudget(); err != nil {
			t.Fatalf("runBudget usage: %v", err)
		}
	})
	if !strings.Contains(usageOut, "Claude monthly") || !strings.Contains(usageOut, "$25.00 / $100.00") {
		t.Fatalf("unexpected usage output:\n%s", usageOut)
	}

	withArgs(t, []string{"tokenmeter", "budget", "delete", "1"})
	deleteOut := captureStdout(t, func() {
		if err := runBudget(); err != nil {
			t.Fatalf("runBudget delete: %v", err)
		}
	})
	if !strings.Contains(deleteOut, "Deleted budget 1") {
		t.Fatalf("unexpected delete output: %s", deleteOut)
	}
	if id != 1 {
		t.Fatalf("expected first budget id to be 1 for CLI output assertions, got %d", id)
	}
	remaining, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("budget was not deleted: %#v", remaining)
	}
}

func TestRunExportWritesOutFile(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Minute)
	seedCLISession(t, db, "export-file", event.PlatformClaude, "/tmp/agmon", "main", now, 10, 5, 0.5)
	outPath := filepath.Join(t.TempDir(), "export.csv")

	withArgs(t, []string{"tokenmeter", "export", "--range", "all", "--format", "csv", "--out", outPath})
	stdout := captureStdout(t, func() {
		if err := runExport(); err != nil {
			t.Fatalf("runExport: %v", err)
		}
	})
	if stdout != "" {
		t.Fatalf("expected no stdout when --out is set, got %q", stdout)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	if !strings.Contains(string(data), "export-file") {
		t.Fatalf("export file missing row:\n%s", string(data))
	}
}
