package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHandleSessionsEmpty(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	var sessions []sessionJSON
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestHandleSessionsWithData(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	db.UpsertSession("s1", event.PlatformClaude, now)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 0.5, now, "src-1")

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var sessions []sessionJSON
	json.Unmarshal(w.Body.Bytes(), &sessions)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "s1" {
		t.Errorf("session ID: got %q", sessions[0].SessionID)
	}
	if sessions[0].InputTokens != 1000 {
		t.Errorf("input tokens: got %d", sessions[0].InputTokens)
	}
}

func TestHandleCosts(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	// Use a time that is definitely in the past
	past := now.Add(-time.Hour)
	db.UpsertSession("s1", event.PlatformClaude, past)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 1.5, past, "src-1")

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/costs", srv.handleCosts)

	req := httptest.NewRequest("GET", "/api/costs?range=all", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d", w.Code)
	}

	var resp costResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Range != "all" {
		t.Errorf("range: got %q", resp.Range)
	}
	if resp.TotalCost < 1.49 {
		t.Errorf("total cost: got %f, want >= 1.49", resp.TotalCost)
	}
	if len(resp.DailyCosts) == 0 {
		t.Error("expected daily costs")
	}
	// "all" range must not return thousands of days from 2020 — it should start
	// from the first token date, so the result is at most a few days.
	if len(resp.DailyCosts) > 60 {
		t.Errorf("all range returned %d days; expected <= 60 (should start from first token date)", len(resp.DailyCosts))
	}
}

func TestHandleStats(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	db.UpsertSession("s1", event.PlatformClaude, now)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 0.5, now, "src-1")

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", srv.handleStats)

	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d", w.Code)
	}

	var resp statsResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalSessions != 1 {
		t.Errorf("total sessions: got %d", resp.TotalSessions)
	}
	if resp.ActiveCount != 1 {
		t.Errorf("active count: got %d", resp.ActiveCount)
	}
}

func TestHandleSessionDetail(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	db.UpsertSession("test-session-abcdef1234567890", event.PlatformClaude, now)
	db.UpdateSessionMeta("test-session-abcdef1234567890", "/Users/test/code/project", "main")
	db.InsertTokenUsage("agent-1", "test-session-abcdef1234567890", 5000, 2000, 100, 200, "claude-sonnet-4-6", 0.35, now, "src-1")
	db.InsertToolCallStart("tc-1", "agent-1", "test-session-abcdef1234567890", "Read", "/some/file.go", now.Add(-10*time.Minute))
	db.UpdateToolCallEnd("tc-1", "file contents...", event.StatusSuccess, 150, now.Add(-9*time.Minute))
	db.InsertToolCallStart("tc-2", "agent-1", "test-session-abcdef1234567890", "Edit", "edit params", now.Add(-8*time.Minute))
	db.UpdateToolCallEnd("tc-2", "edit done", event.StatusSuccess, 300, now.Add(-7*time.Minute))
	db.UpsertAgent("agent-1", "test-session-abcdef1234567890", "", "main", now)
	db.InsertFileChange("test-session-abcdef1234567890", "/project/internal/foo.go", event.FileEdit, now)

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session/", srv.handleSessionDetail)

	// Test with ID prefix
	req := httptest.NewRequest("GET", "/api/session/test-session", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}

	// Parse as raw map to see exact field names
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal top-level: %v", err)
	}

	// Verify all expected top-level keys exist
	expectedKeys := []string{"session", "messages", "tools", "agents", "files", "tool_stats", "agent_stats"}
	for _, key := range expectedKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level key %q in response", key)
		}
	}

	// Verify session fields
	var sess map[string]interface{}
	json.Unmarshal(raw["session"], &sess)
	sessFields := []string{"session_id", "platform", "start_time", "status", "input_tokens", "output_tokens", "cost_usd", "git_branch", "cwd", "model"}
	for _, f := range sessFields {
		if _, ok := sess[f]; !ok {
			t.Errorf("session missing field %q", f)
		}
	}
	if sess["session_id"] != "test-session-abcdef1234567890" {
		t.Errorf("session_id: got %v", sess["session_id"])
	}
	if sess["git_branch"] != "main" {
		t.Errorf("git_branch: got %v", sess["git_branch"])
	}

	// Verify tools array and field names
	var tools []map[string]interface{}
	json.Unmarshal(raw["tools"], &tools)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	toolFields := []string{"call_id", "tool_name", "params", "start_time", "duration_ms", "status"}
	for _, f := range toolFields {
		if _, ok := tools[0][f]; !ok {
			t.Errorf("tool missing field %q", f)
		}
	}
	if tools[0]["tool_name"] != "Read" && tools[0]["tool_name"] != "Edit" {
		t.Errorf("unexpected tool_name: %v", tools[0]["tool_name"])
	}

	// Verify files array and field names
	var files []map[string]interface{}
	json.Unmarshal(raw["files"], &files)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	fileFields := []string{"path", "change_type", "time"}
	for _, f := range fileFields {
		if _, ok := files[0][f]; !ok {
			t.Errorf("file missing field %q", f)
		}
	}

	// Verify agents array
	var agents []map[string]interface{}
	json.Unmarshal(raw["agents"], &agents)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}

	// Verify agent_stats array and field names
	var agentStats []map[string]interface{}
	json.Unmarshal(raw["agent_stats"], &agentStats)
	if len(agentStats) != 1 {
		t.Errorf("expected 1 agent stat, got %d", len(agentStats))
	}
	if len(agentStats) > 0 {
		asFields := []string{"agent_id", "role", "status", "tool_calls", "input_tokens", "output_tokens", "cost_usd"}
		for _, f := range asFields {
			if _, ok := agentStats[0][f]; !ok {
				t.Errorf("agent_stat missing field %q", f)
			}
		}
	}

	// Verify tool_stats array and field names
	var toolStats []map[string]interface{}
	json.Unmarshal(raw["tool_stats"], &toolStats)
	if len(toolStats) == 0 {
		t.Error("expected tool_stats to have entries")
	}
	if len(toolStats) > 0 {
		tsFields := []string{"name", "count", "avg_ms", "fail_count"}
		for _, f := range tsFields {
			if _, ok := toolStats[0][f]; !ok {
				t.Errorf("tool_stat missing field %q", f)
			}
		}
	}

	// Test 404 for non-existent session
	req = httptest.NewRequest("GET", "/api/session/nonexistent999", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404 for nonexistent session, got %d", w.Code)
	}

	// Log full response for debugging
	t.Logf("Full response:\n%s", w.Body.String())
}

func TestStaticFileServing(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Use the same mux setup as the real server
	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<html") {
		t.Error("expected HTML content")
	}
	if !strings.Contains(body, "agmon") {
		t.Error("expected 'agmon' in HTML")
	}
}
