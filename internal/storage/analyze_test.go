package storage

import (
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestAnalyzeEmptyDB(t *testing.T) {
	db := testDB(t)
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)

	result, err := db.Analyze(from, to)
	if err != nil {
		t.Fatalf("analyze empty db: %v", err)
	}
	if result.Cost.Total != 0 || result.Sessions.Total != 0 || len(result.Models) != 0 || len(result.Tools) != 0 {
		t.Fatalf("empty analysis should have zero totals: %#v", result)
	}
	if result.Range == "" || !result.From.Equal(from) || !result.To.Equal(to) {
		t.Fatalf("unexpected range metadata: %#v", result)
	}
}

func TestAnalyzeAggregatesAcrossSessions(t *testing.T) {
	db := testDB(t)
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)

	seedAnalysisSession(t, db, "claude-main", event.PlatformClaude, "/repo/agmon", "main", from.Add(2*time.Hour), "sonnet", 10, "src/main.go", event.StatusSuccess)
	seedAnalysisSession(t, db, "codex-feature", event.PlatformCodex, "/repo/agmon", "feature", from.AddDate(0, 0, 1).Add(3*time.Hour), "gpt-5.5", 5, "README.md", event.StatusFail)
	if err := db.UpsertSession("outside", event.PlatformClaude, to.Add(time.Hour)); err != nil {
		t.Fatalf("upsert outside: %v", err)
	}
	if err := db.InsertTokenUsage("agent-outside", "outside", 100, 50, 0, 0, "sonnet", 99, to.Add(time.Hour), "outside-token"); err != nil {
		t.Fatalf("insert outside token: %v", err)
	}

	result, err := db.Analyze(from, to)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if result.Cost.Total != 15 {
		t.Fatalf("total cost = %f, want 15", result.Cost.Total)
	}
	if result.Cost.ActiveDays != 2 || result.Cost.HighestDayCost != 10 {
		t.Fatalf("unexpected cost summary: %#v", result.Cost)
	}
	if result.Sessions.Total != 2 || result.Sessions.Active != 2 {
		t.Fatalf("unexpected session summary: %#v", result.Sessions)
	}
	if result.Sessions.ByPlatform[string(event.PlatformClaude)] != 1 || result.Sessions.ByPlatform[string(event.PlatformCodex)] != 1 {
		t.Fatalf("unexpected platform counts: %#v", result.Sessions.ByPlatform)
	}
	if result.Sessions.MostExpensive == nil || result.Sessions.MostExpensive.SessionID != "claude-main" {
		t.Fatalf("unexpected most expensive session: %#v", result.Sessions.MostExpensive)
	}
	if len(result.Models) != 2 || result.Models[0].Model != "sonnet" || result.Models[0].CostUSD != 10 {
		t.Fatalf("unexpected models: %#v", result.Models)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "Edit" || result.Tools[0].Count != 2 || result.Tools[0].FailCount != 1 {
		t.Fatalf("unexpected tools: %#v", result.Tools)
	}
	if result.FilesByExt[".go"] != 1 || result.FilesByExt[".md"] != 1 {
		t.Fatalf("unexpected files by ext: %#v", result.FilesByExt)
	}
	if len(result.TopFiles) != 2 || result.TopFiles[0].Path == "" {
		t.Fatalf("unexpected top files: %#v", result.TopFiles)
	}
}

func TestAnalyzeHeatmapBucketsActivity(t *testing.T) {
	db := testDB(t)
	from := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // Monday
	to := from.AddDate(0, 0, 7)
	mondayNineUTC8 := from.Add(time.Hour)
	sundayTwentyThreeUTC8 := from.AddDate(0, 0, 6).Add(15 * time.Hour)

	seedAnalysisSession(t, db, "monday", event.PlatformClaude, "/repo", "main", mondayNineUTC8, "sonnet", 1, "a.go", event.StatusSuccess)
	seedAnalysisSession(t, db, "sunday", event.PlatformCodex, "/repo", "main", sundayTwentyThreeUTC8, "gpt-5.5", 1, "b.go", event.StatusSuccess)

	result, err := db.Analyze(from, to)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if result.Heatmap[0][9] == 0 {
		t.Fatalf("expected Monday 09:00 bucket to be populated: %#v", result.Heatmap[0])
	}
	if result.Heatmap[6][23] == 0 {
		t.Fatalf("expected Sunday 23:00 bucket to be populated: %#v", result.Heatmap[6])
	}
}

func seedAnalysisSession(t *testing.T, db *DB, sessionID string, platform event.Platform, cwd, branch string, ts time.Time, model string, cost float64, filePath string, status event.ToolCallStatus) {
	t.Helper()
	if err := db.UpsertSession(sessionID, platform, ts); err != nil {
		t.Fatalf("upsert %s: %v", sessionID, err)
	}
	if err := db.UpdateSessionMeta(sessionID, cwd, branch); err != nil {
		t.Fatalf("meta %s: %v", sessionID, err)
	}
	agentID := "agent-" + sessionID
	if err := db.UpsertAgent(agentID, sessionID, "", "main", ts); err != nil {
		t.Fatalf("agent %s: %v", sessionID, err)
	}
	if _, err := db.InsertToolCallStart("call-"+sessionID, agentID, sessionID, "Edit", filePath, ts); err != nil {
		t.Fatalf("tool start %s: %v", sessionID, err)
	}
	if err := db.UpdateToolCallEnd("call-"+sessionID, "ok", status, 120, ts.Add(time.Second)); err != nil {
		t.Fatalf("tool end %s: %v", sessionID, err)
	}
	if err := db.InsertFileChange(sessionID, filePath, event.FileEdit, ts.Add(2*time.Second)); err != nil {
		t.Fatalf("file %s: %v", sessionID, err)
	}
	if err := db.InsertTokenUsage(agentID, sessionID, 1000, 250, 0, 0, model, cost, ts.Add(3*time.Second), "token-"+sessionID); err != nil {
		t.Fatalf("token %s: %v", sessionID, err)
	}
}
