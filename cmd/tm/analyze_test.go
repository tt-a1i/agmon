package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestRunAnalyzeTextFormat(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Hour)
	seedAnalyzeCLISession(t, db, "analyze-cli", event.PlatformClaude, now, "sonnet", 2.5, "internal/foo.go")

	withArgs(t, []string{"tokenmeter", "analyze", "--range", "all"})
	out := captureStdout(t, func() {
		if err := runAnalyze(); err != nil {
			t.Fatalf("runAnalyze: %v", err)
		}
	})

	for _, want := range []string{
		"TokenMeter Analysis",
		"Cost",
		"Sessions",
		"Models",
		"Tools (top 5)",
		"Files touched",
		"Activity heatmap",
		"Total:",
		"agmon/main",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("analyze output missing %q:\n%s", want, out)
		}
	}
}

func TestRunAnalyzeJSONFormat(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().Add(-time.Hour)
	seedAnalyzeCLISession(t, db, "analyze-json", event.PlatformCodex, now, "gpt-5.5", 3.75, "README.md")

	withArgs(t, []string{"tokenmeter", "analyze", "--range", "all", "--json"})
	out := captureStdout(t, func() {
		if err := runAnalyze(); err != nil {
			t.Fatalf("runAnalyze: %v", err)
		}
	})

	var payload struct {
		Range string `json:"range"`
		Cost  struct {
			Total float64 `json:"total"`
		} `json:"cost"`
		Sessions struct {
			Total int `json:"total"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid analyze json: %v\n%s", err, out)
	}
	if payload.Cost.Total != 3.75 || payload.Sessions.Total != 1 {
		t.Fatalf("unexpected json payload: %#v", payload)
	}
}

func seedAnalyzeCLISession(t *testing.T, db interface {
	UpsertSession(string, event.Platform, time.Time) error
	UpdateSessionMeta(string, string, string) error
	UpsertAgent(string, string, string, string, time.Time) error
	InsertToolCallStart(string, string, string, string, string, time.Time) (bool, error)
	UpdateToolCallEnd(string, string, event.ToolCallStatus, int64, time.Time) error
	InsertFileChange(string, string, event.FileChangeType, time.Time) error
	InsertTokenUsage(string, string, int, int, int, int, string, float64, time.Time, string) error
}, sessionID string, platform event.Platform, ts time.Time, model string, cost float64, filePath string) {
	t.Helper()
	if err := db.UpsertSession(sessionID, platform, ts); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpdateSessionMeta(sessionID, "/repo/agmon", "main"); err != nil {
		t.Fatalf("update meta: %v", err)
	}
	agentID := "agent-" + sessionID
	if err := db.UpsertAgent(agentID, sessionID, "", "main", ts); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-"+sessionID, agentID, sessionID, "Edit", filePath, ts); err != nil {
		t.Fatalf("insert tool: %v", err)
	}
	if err := db.UpdateToolCallEnd("call-"+sessionID, "ok", event.StatusSuccess, 240, ts.Add(time.Second)); err != nil {
		t.Fatalf("update tool: %v", err)
	}
	if err := db.InsertFileChange(sessionID, filePath, event.FileEdit, ts.Add(2*time.Second)); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := db.InsertTokenUsage(agentID, sessionID, 1000, 250, 0, 0, model, cost, ts.Add(3*time.Second), "token-"+sessionID); err != nil {
		t.Fatalf("insert token: %v", err)
	}
}
