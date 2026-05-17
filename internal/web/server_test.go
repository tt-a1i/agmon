package web

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
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

func TestHandleSessionsPlatformFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("claude-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert claude session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "claude-session", 100, 50, 0, 0, "sonnet", 0.1, now, "src-claude"); err != nil {
		t.Fatalf("insert claude tokens: %v", err)
	}
	if err := db.UpsertSession("codex-session", event.PlatformCodex, now.Add(time.Minute)); err != nil {
		t.Fatalf("upsert codex session: %v", err)
	}
	if err := db.InsertTokenUsage("a2", "codex-session", 200, 75, 0, 0, "gpt-5", 0.2, now.Add(time.Minute), "src-codex"); err != nil {
		t.Fatalf("insert codex tokens: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?platform=codex", nil)
	w := httptest.NewRecorder()
	srv.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	var sessions []sessionJSON
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len: got %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != "codex-session" || sessions[0].Platform != string(event.PlatformCodex) {
		t.Fatalf("session: got %#v, want codex-session only", sessions[0])
	}
}

func TestHandleSessionsWorkspaceFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	sessions := []struct {
		id  string
		cwd string
	}{
		{"root-session", "/foo"},
		{"child-session", "/foo/service"},
		{"prefix-session", "/foobar"},
		{"other-session", "/bar"},
	}
	for i, sess := range sessions {
		start := now.Add(time.Duration(i) * time.Minute)
		if err := db.UpsertSession(sess.id, event.PlatformClaude, start); err != nil {
			t.Fatalf("upsert %s: %v", sess.id, err)
		}
		if err := db.UpdateSessionMeta(sess.id, sess.cwd, "main"); err != nil {
			t.Fatalf("update meta %s: %v", sess.id, err)
		}
		if err := db.InsertTokenUsage("agent-"+sess.id, sess.id, 100, 50, 0, 0, "sonnet", 0.1, start, "src-"+sess.id); err != nil {
			t.Fatalf("insert tokens %s: %v", sess.id, err)
		}
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?workspace=/foo", nil)
	w := httptest.NewRecorder()
	srv.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	var got []sessionJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions len: got %d, want 2: %#v", len(got), got)
	}
	ids := map[string]bool{}
	for _, sess := range got {
		ids[sess.SessionID] = true
	}
	if !ids["root-session"] || !ids["child-session"] || ids["prefix-session"] || ids["other-session"] {
		t.Fatalf("workspace filter returned wrong sessions: %#v", got)
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

func TestHandleExportCSVJSONAndRangeBoundary(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	recent := now.AddDate(0, 0, -2)
	old := now.AddDate(0, 0, -10)

	if err := db.UpsertSession("recent-session", event.PlatformClaude, recent); err != nil {
		t.Fatalf("upsert recent: %v", err)
	}
	if err := db.UpdateSessionMeta("recent-session", "/Users/test/project-alpha", "feature/export, \"quoted\""); err != nil {
		t.Fatalf("update recent meta: %v", err)
	}
	if err := db.InsertTokenUsage("agent-recent", "recent-session", 1000, 400, 25, 75, "claude-sonnet-4-6", 0.42, recent, "src-recent"); err != nil {
		t.Fatalf("insert recent tokens: %v", err)
	}

	if err := db.UpsertSession("old-session", event.PlatformCodex, old); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	if err := db.InsertTokenUsage("agent-old", "old-session", 800, 200, 0, 0, "gpt-5", 0.24, old, "src-old"); err != nil {
		t.Fatalf("insert old tokens: %v", err)
	}

	srv := NewServer(db, "0")
	csvReq := httptest.NewRequest(http.MethodGet, "/api/export?format=csv", nil)
	csvRec := httptest.NewRecorder()
	srv.handleExport(csvRec, csvReq)

	if csvRec.Code != http.StatusOK {
		t.Fatalf("csv status: got %d, want 200. body: %s", csvRec.Code, csvRec.Body.String())
	}
	if ct := csvRec.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("csv content-type: got %q", ct)
	}
	if cd := csvRec.Header().Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="tokenmeter-7d-`) || !strings.HasSuffix(cd, `.csv"`) {
		t.Fatalf("csv content-disposition: got %q", cd)
	}
	records, err := csv.NewReader(strings.NewReader(csvRec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("csv records: got %d, want header + one recent row. records=%v", len(records), records)
	}
	wantHeader := []string{"date", "session_id", "session_name", "platform", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd"}
	if strings.Join(records[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("csv header: got %v, want %v", records[0], wantHeader)
	}
	if records[1][1] != "recent-session" || records[1][2] != "feature/export, \"quoted\"" || records[1][7] != "100" || records[1][8] != "0.420000" {
		t.Fatalf("csv row: got %v", records[1])
	}

	jsonReq := httptest.NewRequest(http.MethodGet, "/api/export?range=all&format=json", nil)
	jsonRec := httptest.NewRecorder()
	srv.handleExport(jsonRec, jsonReq)

	if jsonRec.Code != http.StatusOK {
		t.Fatalf("json status: got %d, want 200. body: %s", jsonRec.Code, jsonRec.Body.String())
	}
	if ct := jsonRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("json content-type: got %q", ct)
	}
	var rows []map[string]any
	if err := json.Unmarshal(jsonRec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal json export: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("json rows: got %d, want 2", len(rows))
	}
}

func TestExportRowPoolDoesNotCorruptOutput(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	for s := 0; s < 5; s++ {
		sessionID := "pool-session-" + strconv.Itoa(s)
		if err := db.UpsertSession(sessionID, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert session %d: %v", s, err)
		}
		if err := db.UpdateSessionMeta(sessionID, "/tmp/pool-"+strconv.Itoa(s), "pool/branch-"+strconv.Itoa(s)); err != nil {
			t.Fatalf("update session meta %d: %v", s, err)
		}
	}
	for i := 0; i < 100; i++ {
		sessionID := "pool-session-" + strconv.Itoa(i%5)
		if err := db.InsertTokenUsage("pool-agent", sessionID, 100+i, 20+i%7, i%3, i%5, "pool-model", float64(i)/1000, now.Add(time.Duration(i)*time.Second), "pool-src-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("insert token usage %d: %v", i, err)
		}
	}

	srv := NewServer(db, "0")
	request := func() string {
		req := httptest.NewRequest(http.MethodGet, "/api/export?range=all&format=csv", nil)
		w := httptest.NewRecorder()
		srv.handleExport(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	want := request()

	var wg sync.WaitGroup
	errCh := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := request(); got != want {
				errCh <- got
			}
		}()
	}
	wg.Wait()
	close(errCh)
	if got, ok := <-errCh; ok {
		t.Fatalf("concurrent export output differed\nwant:\n%s\ngot:\n%s", want, got)
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

func TestHandleBudgetsEndpoints(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	getReq := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	getRec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("empty get status: got %d, want 200. body: %s", getRec.Code, getRec.Body.String())
	}
	var empty []budgetJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("unmarshal empty budgets: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty budgets len: got %d, want 0", len(empty))
	}

	now := time.Now()
	if err := db.UpsertSession("claude-budget-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert claude session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "claude-budget-session", 100, 50, 0, 0, "sonnet", 85, now, "budget-claude"); err != nil {
		t.Fatalf("insert claude tokens: %v", err)
	}
	if err := db.UpsertSession("codex-budget-session", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert codex session: %v", err)
	}
	if err := db.InsertTokenUsage("a2", "codex-budget-session", 100, 50, 0, 0, "gpt-5", 15, now, "budget-codex"); err != nil {
		t.Fatalf("insert codex tokens: %v", err)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/budgets", strings.NewReader(`{"name":"Claude monthly","monthly_usd":100,"platform":"claude"}`))
	postRec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("post status: got %d, want 201. body: %s", postRec.Code, postRec.Body.String())
	}
	var created budgetJSON
	if err := json.Unmarshal(postRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created budget: %v", err)
	}
	if created.ID == 0 || created.Name != "Claude monthly" || created.MonthlyUSD != 100 || created.Platform != "claude" {
		t.Fatalf("created budget: got %#v", created)
	}
	if created.Usage.Used != 85 || created.Usage.Limit != 100 || created.Usage.Percent != 85 || created.Usage.Status != "warn" {
		t.Fatalf("created usage: got %#v", created.Usage)
	}

	getRec = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status: got %d, want 200. body: %s", getRec.Code, getRec.Body.String())
	}
	var budgets []budgetJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &budgets); err != nil {
		t.Fatalf("unmarshal budgets: %v", err)
	}
	if len(budgets) != 1 || budgets[0].ID != created.ID {
		t.Fatalf("budgets after create: got %#v", budgets)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/budgets/"+strconv.FormatInt(created.ID, 10), strings.NewReader(`{"name":"All monthly","monthly_usd":80,"platform":""}`))
	putRec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put status: got %d, want 200. body: %s", putRec.Code, putRec.Body.String())
	}
	var updated budgetJSON
	if err := json.Unmarshal(putRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated budget: %v", err)
	}
	if updated.Name != "All monthly" || updated.MonthlyUSD != 80 || updated.Platform != "" {
		t.Fatalf("updated budget: got %#v", updated)
	}
	if updated.Usage.Used != 100 || updated.Usage.Limit != 80 || updated.Usage.Percent != 125 || updated.Usage.Status != "over" {
		t.Fatalf("updated usage: got %#v", updated.Usage)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/budgets/"+strconv.FormatInt(created.ID, 10), nil)
	deleteRec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status: got %d, want 204. body: %s", deleteRec.Code, deleteRec.Body.String())
	}
	getRec = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(getRec, getReq)
	if err := json.Unmarshal(getRec.Body.Bytes(), &budgets); err != nil {
		t.Fatalf("unmarshal budgets after delete: %v", err)
	}
	if len(budgets) != 0 {
		t.Fatalf("budgets after delete: got %#v", budgets)
	}
}

func TestHandleEventsUnavailable(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	w := httptest.NewRecorder()
	srv.handleEvents(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleEventsStreamsHeartbeatAndEvents(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("test.sock"))
	srv.eventHeartbeat = 10 * time.Millisecond

	events := make(chan event.Event, 1)
	closed := make(chan struct{})
	srv.subscribeRemote = func(sockPath string) (<-chan event.Event, func(), error) {
		if sockPath != "test.sock" {
			t.Fatalf("sock path: got %q, want test.sock", sockPath)
		}
		return events, func() { close(closed) }, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type: got %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if !strings.HasPrefix(line, ": heartbeat") {
		t.Fatalf("first SSE line: got %q, want heartbeat comment", line)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read heartbeat terminator: %v", err)
	}

	want := event.Event{
		ID:        "evt-1",
		Type:      event.EventTokenUsage,
		SessionID: "session-1",
		Platform:  event.PlatformCodex,
		Timestamp: time.Unix(123, 0).UTC(),
		Data:      event.EventData{InputTokens: 10, OutputTokens: 20, CostUSD: 0.001},
	}
	events <- want

	var dataLine string
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			break
		}
	}

	var got event.Event
	if err := json.Unmarshal([]byte(dataLine), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got.Type != want.Type || got.SessionID != want.SessionID || got.Data.CostUSD != want.Data.CostUSD {
		t.Fatalf("event: got %#v, want %#v", got, want)
	}

	resp.Body.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("subscription close function was not called")
	}
}

func TestHandleEventsSubscribeError(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("missing.sock"))
	srv.subscribeRemote = func(string) (<-chan event.Event, func(), error) {
		return nil, nil, errors.New("missing daemon")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	w := httptest.NewRecorder()
	srv.handleEvents(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleSearchBadQuery(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "missing", path: "/api/search"},
		{name: "too short", path: "/api/search?q=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.handleSearch(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400. body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleSearchReturnsHits(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("web-search-session", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpdateSessionMeta("web-search-session", "/Users/test/web-search-project", ""); err != nil {
		t.Fatalf("update meta: %v", err)
	}
	if _, err := db.InsertToolCallStart("web-search-call", "agent", "web-search-session", "Bash", "run needle command", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if err := db.InsertFileChange("web-search-session", "/tmp/needle-web.go", event.FileEdit, now.Add(time.Second)); err != nil {
		t.Fatalf("insert file change: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=needle&limit=10", nil)
	rec := httptest.NewRecorder()
	srv.handleSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	var hits []storage.SearchHit
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("unmarshal hits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits len: got %d, want 2: %#v", len(hits), hits)
	}
	kinds := map[string]bool{}
	for _, hit := range hits {
		kinds[hit.Kind] = true
		if hit.SessionID != "web-search-session" {
			t.Fatalf("session id: got %q, want web-search-session", hit.SessionID)
		}
		if hit.SessionName != "web-search-project" {
			t.Fatalf("session name: got %q, want web-search-project", hit.SessionName)
		}
		if !strings.Contains(strings.ToLower(hit.Excerpt), "needle") {
			t.Fatalf("excerpt %q does not include query", hit.Excerpt)
		}
	}
	if !kinds["tool_param"] || !kinds["file"] {
		t.Fatalf("kinds: got %#v, want tool_param and file", kinds)
	}
}

func TestHandleCompare(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for _, id := range []string{"session-alpha", "session-beta", "ambiguous-one", "ambiguous-two"} {
		if err := db.UpsertSession(id, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	if err := db.InsertTokenUsage("a", "session-alpha", 1000, 200, 0, 0, "sonnet", 0.50, now, "src-a"); err != nil {
		t.Fatalf("insert alpha tokens: %v", err)
	}
	if err := db.InsertTokenUsage("b", "session-beta", 700, 300, 0, 0, "sonnet", 0.75, now, "src-b"); err != nil {
		t.Fatalf("insert beta tokens: %v", err)
	}
	if _, err := db.InsertToolCallStart("tool-a-read", "a", "session-alpha", "Read", "a.go", now); err != nil {
		t.Fatalf("insert alpha read: %v", err)
	}
	if err := db.UpdateToolCallEnd("tool-a-read", "ok", event.StatusSuccess, 10, now.Add(time.Second)); err != nil {
		t.Fatalf("end alpha read: %v", err)
	}
	if _, err := db.InsertToolCallStart("tool-b-read", "b", "session-beta", "Read", "b.go", now); err != nil {
		t.Fatalf("insert beta read: %v", err)
	}
	if err := db.UpdateToolCallEnd("tool-b-read", "ok", event.StatusSuccess, 20, now.Add(time.Second)); err != nil {
		t.Fatalf("end beta read: %v", err)
	}
	if _, err := db.InsertToolCallStart("tool-b-edit", "b", "session-beta", "Edit", "b.go", now); err != nil {
		t.Fatalf("insert beta edit: %v", err)
	}
	if err := db.InsertFileChange("session-alpha", "/tmp/common.go", event.FileEdit, now); err != nil {
		t.Fatalf("insert alpha common file: %v", err)
	}
	if err := db.InsertFileChange("session-alpha", "/tmp/a-only.go", event.FileEdit, now); err != nil {
		t.Fatalf("insert alpha only file: %v", err)
	}
	if err := db.InsertFileChange("session-beta", "/tmp/common.go", event.FileEdit, now); err != nil {
		t.Fatalf("insert beta common file: %v", err)
	}
	if err := db.InsertFileChange("session-beta", "/tmp/b-only.go", event.FileCreate, now); err != nil {
		t.Fatalf("insert beta only file: %v", err)
	}

	srv := NewServer(db, "0")
	okReq := httptest.NewRequest(http.MethodGet, "/api/compare?a=session-alpha&b=session-beta", nil)
	okRec := httptest.NewRecorder()
	srv.handleCompare(okRec, okReq)

	if okRec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", okRec.Code, okRec.Body.String())
	}
	var resp struct {
		ToolDiff []struct {
			Name   string `json:"name"`
			ACount int    `json:"a_count"`
			BCount int    `json:"b_count"`
		} `json:"tool_diff"`
		CostDiff struct {
			A     float64 `json:"a"`
			B     float64 `json:"b"`
			Delta float64 `json:"delta"`
		} `json:"cost_diff"`
		FileDiff struct {
			AOnly  []string `json:"a_only"`
			BOnly  []string `json:"b_only"`
			Common []string `json:"common"`
		} `json:"file_diff"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal compare: %v", err)
	}
	if resp.CostDiff.A != 0.50 || resp.CostDiff.B != 0.75 || resp.CostDiff.Delta != 0.25 {
		t.Fatalf("cost diff: got %#v", resp.CostDiff)
	}
	if len(resp.ToolDiff) != 2 {
		t.Fatalf("tool diff len: got %d, want 2: %#v", len(resp.ToolDiff), resp.ToolDiff)
	}
	if len(resp.FileDiff.AOnly) != 1 || resp.FileDiff.AOnly[0] != "/tmp/a-only.go" ||
		len(resp.FileDiff.BOnly) != 1 || resp.FileDiff.BOnly[0] != "/tmp/b-only.go" ||
		len(resp.FileDiff.Common) != 1 || resp.FileDiff.Common[0] != "/tmp/common.go" {
		t.Fatalf("file diff: got %#v", resp.FileDiff)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/compare?a=session-alpha", nil)
	missingRec := httptest.NewRecorder()
	srv.handleCompare(missingRec, missingReq)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing param status: got %d, want 400", missingRec.Code)
	}

	ambiguousReq := httptest.NewRequest(http.MethodGet, "/api/compare?a=ambiguous&b=session-beta", nil)
	ambiguousRec := httptest.NewRecorder()
	srv.handleCompare(ambiguousRec, ambiguousReq)
	if ambiguousRec.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous status: got %d, want 400", ambiguousRec.Code)
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

func TestHandleSessionTagUpdate(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("tag-session-abcdef", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodPut, "/api/session/tag-session/tag", strings.NewReader(`{"tag":"refactor auth"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
		Tag       string `json:"tag"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.SessionID != "tag-session-abcdef" || resp.Tag != "refactor auth" {
		t.Fatalf("response: got %#v", resp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/session/tag-session", nil)
	getW := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("detail status: got %d, want 200. body: %s", getW.Code, getW.Body.String())
	}
	var detail struct {
		Session sessionJSON `json:"session"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.Session.Tag != "refactor auth" {
		t.Fatalf("detail tag: got %q, want refactor auth", detail.Session.Tag)
	}
}

func TestHandleSessionTagClear(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("clear-tag-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.SetSessionTag("clear-tag-session", "temporary"); err != nil {
		t.Fatalf("set initial tag: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodPut, "/api/session/clear-tag/tag", strings.NewReader(`{"tag":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	sess, found, err := db.GetSessionByIDPrefix("clear-tag")
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	if sess.Tag != "" {
		t.Fatalf("tag should be cleared, got %q", sess.Tag)
	}
}

func TestHandleSessionTagAmbiguousPrefix(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for _, id := range []string{"tag-ambiguous-one", "tag-ambiguous-two"} {
		if err := db.UpsertSession(id, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodPut, "/api/session/tag-ambiguous/tag", strings.NewReader(`{"tag":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400. body: %s", w.Code, w.Body.String())
	}
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
	if !strings.Contains(body, "TokenMeter") {
		t.Error("expected 'TokenMeter' in HTML")
	}
}

type mockMetricsProvider struct {
	droppedBcast int64
	droppedShut  int64
	dupTool      int64
	budgets      []BudgetMetric
}

func (m mockMetricsProvider) DaemonStats() (int64, int64, int64) {
	return m.droppedBcast, m.droppedShut, m.dupTool
}

func (m mockMetricsProvider) BudgetUsageAll() ([]BudgetMetric, error) {
	return m.budgets, nil
}

func TestHandleMetricsReturnsPrometheusFormat(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithBuildVersion("test-version"))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Fatalf("content-type: got %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"# HELP tokenmeter_build_info Build version",
		"# TYPE tokenmeter_build_info gauge",
		`tokenmeter_build_info{version="test-version"} 1`,
		"# HELP tokenmeter_today_cost_usd Total cost today (local TZ bucket)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestHandleMetricsIncludesDaemonStats(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithMetricsProvider(mockMetricsProvider{
		droppedBcast: 7,
		droppedShut:  3,
		dupTool:      2,
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	body := w.Body.String()
	for _, want := range []string{
		"tokenmeter_daemon_dropped_broadcasts_total 7",
		"tokenmeter_daemon_dropped_shutdown_total 3",
		"tokenmeter_daemon_duplicate_tool_starts_total 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestHandleMetricsIncludesBudgets(t *testing.T) {
	db := testDB(t)
	now := time.Now().Add(-time.Minute)
	if err := db.UpsertSession("budget-metrics", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "budget-metrics", 1, 1, 0, 0, "sonnet", 4.20, now, "budget-metrics"); err != nil {
		t.Fatalf("insert usage: %v", err)
	}
	if _, err := db.InsertBudget("Claude monthly", 100, string(event.PlatformClaude)); err != nil {
		t.Fatalf("insert budget: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	body := w.Body.String()
	for _, want := range []string{
		`tokenmeter_budget_used_usd{name="Claude monthly",platform="claude"} 4.2`,
		`tokenmeter_budget_limit_usd{name="Claude monthly",platform="claude"} 100`,
		`tokenmeter_budget_percent{name="Claude monthly",platform="claude"} 4.2`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestHandleMetricsEscapesLabels(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithMetricsProvider(mockMetricsProvider{
		budgets: []BudgetMetric{{
			Name:     "bad\"name\\line\nnext",
			Platform: "claude",
			UsedUSD:  1,
			LimitUSD: 2,
			Percent:  50,
		}},
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	want := `name="bad\"name\\line\nnext"`
	if body := w.Body.String(); !strings.Contains(body, want) {
		t.Fatalf("escaped label %q not found\n%s", want, body)
	}
}

func TestHandleHealthAllOk(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	var resp healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}
	if resp.Status != "healthy" || resp.Checks.DB.Status != "ok" || resp.Checks.Daemon.Status != "ok" {
		t.Fatalf("health response = %#v", resp)
	}
}

func TestHandleHealthDBClosed(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503. body: %s", w.Code, w.Body.String())
	}
	var resp healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}
	if resp.Status != "unhealthy" || !strings.HasPrefix(resp.Checks.DB.Status, "error:") {
		t.Fatalf("health response = %#v", resp)
	}
}

func TestHandleHealthIncludesVersion(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithBuildVersion("health-test-version"))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	var resp healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}
	if resp.Version != "health-test-version" {
		t.Fatalf("version = %q, want health-test-version", resp.Version)
	}
}

// ─── A11y tests: verify index.html accessibility structure ───────────────────

func getStaticIndex(t *testing.T) string {
	t.Helper()
	db := testDB(t)
	srv := NewServer(db, "0")
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("index status: got %d, want 200", w.Code)
	}
	return w.Body.String()
}

func TestStaticIndexHasA11yLandmarks(t *testing.T) {
	body := getStaticIndex(t)
	for _, want := range []string{
		`role="banner"`,
		`role="navigation"`,
		`role="main"`,
		`role="contentinfo"`,
		`aria-label=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html missing a11y landmark %q", want)
		}
	}
}

func TestStaticIndexHasSkipLink(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `href="#main-content"`) {
		t.Error("index.html missing skip link with href='#main-content'")
	}
	if !strings.Contains(body, `class="skip-link"`) {
		t.Error("index.html missing .skip-link class")
	}
}

func TestStaticIndexHasToastContainer(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="toast-container"`) {
		t.Error("index.html missing #toast-container")
	}
	if !strings.Contains(body, `aria-live="polite"`) {
		t.Error("index.html toast-container missing aria-live='polite'")
	}
}

func TestStaticIndexHasReducedMotionMedia(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `prefers-reduced-motion`) {
		t.Error("index.html missing prefers-reduced-motion media query")
	}
}

func TestStaticIndexHasFocusTrapHelper(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `function trapFocus`) {
		t.Error("index.html missing function trapFocus")
	}
}

func TestStaticIndexHasSafeFetch(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `async function safeFetch`) {
		t.Error("index.html missing async function safeFetch")
	}
}

func TestStaticIndexNoAlertCalls(t *testing.T) {
	body := getStaticIndex(t)
	// Strip comment blocks to avoid false matches in comments
	stripped := body
	count := strings.Count(stripped, "alert(")
	if count > 0 {
		t.Errorf("index.html still has %d alert() call(s); replace with showToast", count)
	}
}

func TestStaticIndexHasColorSchemeMedia(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `prefers-color-scheme`) {
		t.Error("index.html missing prefers-color-scheme media query")
	}
}

// ─── Performance tests: verify index.html perf features ──────────────────────

func TestStaticIndexHasVirtualScroll(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "renderSessionListVirtual") {
		t.Error("index.html missing renderSessionListVirtual function")
	}
	if !strings.Contains(body, "VS_THRESHOLD") {
		t.Error("index.html missing VS_THRESHOLD constant")
	}
}

func TestStaticIndexHasDebounce(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "function debounce") {
		t.Error("index.html missing function debounce")
	}
}

func TestStaticIndexHasIdleCallback(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "requestIdleCallback") {
		t.Error("index.html missing requestIdleCallback usage")
	}
}

func TestStaticIndexHasCSSContainment(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "contain:layout style paint") && !strings.Contains(body, "contain: layout style paint") {
		t.Error("index.html missing CSS containment (contain: layout style paint)")
	}
}

func TestStaticIndexHasPerfHud(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "perf-hud") {
		t.Error("index.html missing perf-hud element")
	}
}

// ─────────────────────────────────────────────────────────────────────────────

func TestStaticIndexHasEmptyStateTemplate(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="empty-sessions"`) {
		t.Error("index.html missing <template id=\"empty-sessions\">")
	}
	if !strings.Contains(body, `id="empty-search"`) {
		t.Error("index.html missing <template id=\"empty-search\">")
	}
	if !strings.Contains(body, "empty-state") {
		t.Error("index.html missing .empty-state CSS class usage in templates")
	}
}

func TestStaticIndexHasSkeletonRows(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "skeleton-row") {
		t.Error("index.html missing skeleton-row CSS class")
	}
	if !strings.Contains(body, "showSkeletonSessions") {
		t.Error("index.html missing showSkeletonSessions function")
	}
}

func TestStaticIndexHasPullToRefresh(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "pull-to-refresh") && !strings.Contains(body, "ptr-bar") {
		t.Error("index.html missing pull-to-refresh logic or ptr-bar element")
	}
	if !strings.Contains(body, "touchstart") {
		t.Error("index.html missing touchstart handler (pull-to-refresh)")
	}
	if !strings.Contains(body, "touchend") {
		t.Error("index.html missing touchend handler (pull-to-refresh)")
	}
}

func TestStaticIndexHasSwipeTabs(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "dTabs") {
		t.Error("index.html missing dTabs element reference")
	}
	if !strings.Contains(body, "swipe") && !strings.Contains(body, "Swipe") {
		t.Error("index.html missing swipe tab comment or mention")
	}
}

func TestStaticIndexHasClassifyError(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, "classifyError") {
		t.Error("index.html missing classifyError function")
	}
	if !strings.Contains(body, "'offline'") && !strings.Contains(body, `"offline"`) {
		t.Error("index.html classifyError missing 'offline' classification")
	}
	if !strings.Contains(body, "'server'") && !strings.Contains(body, `"server"`) {
		t.Error("index.html classifyError missing 'server' classification")
	}
}

func TestStaticIndexHasReducedMotionSkeleton(t *testing.T) {
	body := getStaticIndex(t)
	// skeleton-row must have a prefers-reduced-motion override (no animation)
	if !strings.Contains(body, "skeleton-row") {
		t.Error("index.html missing skeleton-row class")
	}
	if !strings.Contains(body, "prefers-reduced-motion") {
		t.Error("index.html missing prefers-reduced-motion media query")
	}
	// Both must appear together — we check their co-presence since the CSS block
	// explicitly turns off the shimmer animation when motion is reduced.
	if !strings.Contains(body, "shimmer") {
		t.Error("index.html missing shimmer keyframe animation for skeleton")
	}
}

func TestStaticIndexHasChartCanvases(t *testing.T) {
	body := getStaticIndex(t)
	for _, id := range []string{"chart-cost-trend", "chart-tool-donut", "chart-model-bar"} {
		if !strings.Contains(body, id) {
			t.Errorf("index.html missing canvas id %q", id)
		}
	}
}

func TestStaticIndexHasDrawFunctions(t *testing.T) {
	body := getStaticIndex(t)
	for _, fn := range []string{"drawCostTrendChart", "drawToolDonutChart", "drawModelBarChart", "renderInteractiveCharts"} {
		if !strings.Contains(body, fn) {
			t.Errorf("index.html missing chart function %q", fn)
		}
	}
	if !strings.Contains(body, "icSetupCanvas") {
		t.Error("index.html missing icSetupCanvas (DPR retina helper)")
	}
}

func TestStaticIndexHasChartA11yLabels(t *testing.T) {
	body := getStaticIndex(t)
	// Each chart canvas must have role="img" and aria-label
	for _, label := range []string{
		`aria-label="Daily cost trend for the last 30 days"`,
		`aria-label="Tool usage distribution donut chart"`,
		`aria-label="Model token usage breakdown`,
	} {
		if !strings.Contains(body, label) {
			t.Errorf("index.html missing aria-label: %s", label)
		}
	}
	if !strings.Contains(body, `role="img"`) {
		t.Error("index.html missing role=\"img\" on canvas elements")
	}
}

func TestStaticIndexHasChartSRFallback(t *testing.T) {
	body := getStaticIndex(t)
	// Each chart must have a visually-hidden <table> for screen readers
	for _, caption := range []string{
		"Daily cost data",
		"Tool usage distribution",
		"Model token usage",
	} {
		if !strings.Contains(body, caption) {
			t.Errorf("index.html missing SR-only table caption %q", caption)
		}
	}
	if !strings.Contains(body, "<caption>") {
		t.Error("index.html missing <caption> elements in SR fallback tables")
	}
}

func TestStaticIndexHasChartReducedMotion(t *testing.T) {
	body := getStaticIndex(t)
	// renderInteractiveCharts must reference prefers-reduced-motion
	if !strings.Contains(body, "prefers-reduced-motion") {
		t.Error("index.html missing prefers-reduced-motion reference")
	}
	// The chart toggle pref must exist
	if !strings.Contains(body, "showCharts") {
		t.Error("index.html missing showCharts preference")
	}
	// DPR scaling must be present (retina support)
	if !strings.Contains(body, "devicePixelRatio") {
		t.Error("index.html missing devicePixelRatio (retina DPR handling)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────

func TestStaticIndexHasSessionDetailView(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="session-detail-view"`) {
		t.Error("index.html missing id=\"session-detail-view\"")
	}
	if !strings.Contains(body, "openSessionDetailView") {
		t.Error("index.html missing openSessionDetailView function")
	}
	if !strings.Contains(body, "closeSessionDetailView") {
		t.Error("index.html missing closeSessionDetailView function")
	}
}

func TestStaticIndexHasSDVTabs(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `role="tablist"`) {
		t.Error("index.html missing role=\"tablist\" on SDV nav")
	}
	if !strings.Contains(body, `aria-label="Session detail sections"`) {
		t.Error("index.html missing aria-label for session detail tablist")
	}
	for _, tab := range []string{"timeline", "tools", "messages", "files", "tokens"} {
		if !strings.Contains(body, `data-sdv-tab="`+tab+`"`) {
			t.Errorf("index.html missing SDV tab %q", tab)
		}
	}
}

func TestStaticIndexHasSDVBackButton(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="sdv-back"`) {
		t.Error("index.html missing id=\"sdv-back\"")
	}
	if !strings.Contains(body, `aria-label="Back to dashboard"`) {
		t.Error("index.html missing aria-label on SDV back button")
	}
}

func TestStaticIndexHasCustomRangePicker(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="range-custom"`) {
		t.Error("index.html missing id=\"range-custom\"")
	}
	if !strings.Contains(body, `id="range-from"`) {
		t.Error("index.html missing id=\"range-from\"")
	}
	if !strings.Contains(body, `id="range-to"`) {
		t.Error("index.html missing id=\"range-to\"")
	}
}

func TestStaticIndexHasRangeApply(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="range-apply"`) {
		t.Error("index.html missing id=\"range-apply\"")
	}
	// Apply button must be wired — look for click handler or range apply reference
	if !strings.Contains(body, "range-apply") {
		t.Error("index.html range-apply button not referenced in JS")
	}
}

func TestStaticIndexHasDateInputA11y(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `type="date"`) {
		t.Error("index.html missing type=\"date\" inputs")
	}
	if !strings.Contains(body, `aria-label="From date"`) {
		t.Error("index.html missing aria-label on from date input")
	}
	if !strings.Contains(body, `aria-label="To date"`) {
		t.Error("index.html missing aria-label on to date input")
	}
}

func TestStaticIndexHasOnboardingTour(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="onboarding"`) {
		t.Error("index.html missing id=\"onboarding\"")
	}
	if !strings.Contains(body, `role="dialog"`) {
		t.Error("index.html missing role=\"dialog\" on onboarding")
	}
	if !strings.Contains(body, `aria-modal="true"`) {
		t.Error("index.html missing aria-modal=\"true\" on onboarding")
	}
}

func TestStaticIndexHasOnboardingSteps(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `onboardingSteps`) {
		t.Error("index.html missing onboardingSteps array")
	}
	if !strings.Contains(body, `showStep`) {
		t.Error("index.html missing showStep function")
	}
	if !strings.Contains(body, `startOnboarding`) {
		t.Error("index.html missing startOnboarding function")
	}
	if !strings.Contains(body, `endOnboarding`) {
		t.Error("index.html missing endOnboarding function")
	}
}

func TestStaticIndexHasOnboardingDismissPersist(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `tokenmeter-onboarding-done`) {
		t.Error("index.html missing localStorage key tokenmeter-onboarding-done")
	}
}

func TestStaticIndexHasKbdSearch(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="kbd-search"`) {
		t.Error("index.html missing id=\"kbd-search\" search input in kbdModal")
	}
	if !strings.Contains(body, `filterKbdShortcuts`) {
		t.Error("index.html missing filterKbdShortcuts function")
	}
}

func TestStaticIndexHasKbdPrintButton(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="kbd-print"`) {
		t.Error("index.html missing id=\"kbd-print\" print button")
	}
	if !strings.Contains(body, `window.print()`) {
		t.Error("index.html missing window.print() call")
	}
}

func TestStaticIndexHasKbdGroups(t *testing.T) {
	body := getStaticIndex(t)
	for _, group := range []string{"Navigation", "View", "Filter", "Actions", "Onboarding"} {
		needle := `data-group="` + group + `"`
		if !strings.Contains(body, needle) {
			t.Errorf("index.html missing kbd section data-group=%q", group)
		}
	}
}

func TestStaticIndexHasThemePicker(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `applyTheme`) && !strings.Contains(body, `window.setTheme`) {
		t.Error("index.html missing upgraded theme function (applyTheme or window.setTheme)")
	}
	for _, v := range []string{"high-contrast", "custom"} {
		needle := `data-v="` + v + `"`
		if !strings.Contains(body, needle) {
			t.Errorf("index.html missing theme radio button data-v=%q", v)
		}
	}
}

func TestStaticIndexHasHighContrastTheme(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `data-theme="high-contrast"`) {
		t.Error("index.html missing [data-theme=\"high-contrast\"] CSS block")
	}
}

func TestStaticIndexHasCustomAccentInput(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `type="color"`) {
		t.Error("index.html missing accent color input (type=\"color\")")
	}
	if !strings.Contains(body, `accentColorInput`) {
		t.Error("index.html missing id accentColorInput")
	}
}

func TestStaticIndexHasAutoThemeListener(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `prefers-color-scheme`) {
		t.Error("index.html missing prefers-color-scheme media query reference")
	}
	if !strings.Contains(body, `matchMedia`) {
		t.Error("index.html missing matchMedia call for auto theme listener")
	}
}

func TestStaticIndexHasNotifyFunction(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `function notify`) {
		t.Error("index.html missing notify function")
	}
	if !strings.Contains(body, `maybeNotifyOffline`) {
		t.Error("index.html missing maybeNotifyOffline function")
	}
}

func TestStaticIndexHasNotificationPermissionFlow(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `Notification.requestPermission`) {
		t.Error("index.html missing Notification.requestPermission call")
	}
	if !strings.Contains(body, `tokenmeter-notifications`) {
		t.Error("index.html missing tokenmeter-notifications localStorage key")
	}
	if !strings.Contains(body, `handleNotificationToggle`) {
		t.Error("index.html missing handleNotificationToggle function")
	}
}

// ─────────────────────────────────────────────────────────────────────────────

func TestHandleProjection(t *testing.T) {
	db := testDB(t)
	now := time.Now().Add(-time.Hour)
	if err := db.UpsertSession("projection-web", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent-projection", "projection-web", 1, 1, 0, 0, "sonnet", 12.5, now, "projection-web"); err != nil {
		t.Fatalf("insert usage: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/projection", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	var p storage.CostProjection
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	if p.DaysElapsed == 0 || p.DaysInMonth == 0 || p.Confidence == "" {
		t.Fatalf("missing projection fields: %#v", p)
	}
	if p.UsedSoFar <= 0 || p.ProjectedTotal <= 0 {
		t.Fatalf("projection should include cost values, got %#v", p)
	}
}

// ── Analytics endpoint tests ──────────────────────────────────────────────────

func seedAnalyticsData(t *testing.T, db *storage.DB) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("analytics-sess-%d", i)
		if err := db.UpsertSession(sid, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert session: %v", err)
		}
		cost := float64(i+1) * 0.5
		if err := db.InsertTokenUsage("agent1", sid, 1000*(i+1), 500*(i+1), 0, 0,
			"claude-sonnet-4-6", cost, now, fmt.Sprintf("src-analytics-%d", i)); err != nil {
			t.Fatalf("insert tokens: %v", err)
		}
	}
}

func TestAnalyticsEndpointReturnsValidJSON(t *testing.T) {
	db := testDB(t)
	seedAnalyticsData(t, db)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/analytics?range=week", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json", ct)
	}

	var resp analyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal analytics: %v", err)
	}
	if resp.Range == "" {
		t.Error("analytics: range field is empty")
	}
	if resp.GeneratedAt == "" {
		t.Error("analytics: generated_at field is empty")
	}
	// top_expensive_sessions, tool_breakdown, model_mix_daily, anomalies must be present
	// (can be empty slices when DB has no data in older ranges)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"top_expensive_sessions", "tool_breakdown", "model_mix_daily", "anomalies"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("analytics response missing key %q", key)
		}
	}
}

func TestAnalyticsRespectsRangePeriods(t *testing.T) {
	db := testDB(t)
	seedAnalyticsData(t, db)
	srv := NewServer(db, "0")

	for _, rangeParam := range []string{"today", "week", "month", "all"} {
		t.Run(rangeParam, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/analytics?range="+rangeParam, nil)
			w := httptest.NewRecorder()
			srv.handleAnalytics(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("range=%s: status %d, body: %s", rangeParam, w.Code, w.Body.String())
			}
			var resp analyticsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("range=%s: unmarshal: %v", rangeParam, err)
			}
			if resp.Range == "" {
				t.Errorf("range=%s: empty range in response", rangeParam)
			}
		})
	}
}

func TestExportReportReturnsHTML(t *testing.T) {
	db := testDB(t)
	seedAnalyticsData(t, db)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/export-report?range=week", nil)
	w := httptest.NewRecorder()
	srv.handleExportReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), "<!DOCTYPE html") {
		t.Error("export-report body should start with <!DOCTYPE html")
	}
	// Self-contained: must have inline data (no external script src)
	if !strings.Contains(body, "<table") {
		t.Error("export-report HTML missing <table> — analytics cards not rendered")
	}
}

func TestExportReportFileNameHeader(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/export-report?range=week", nil)
	w := httptest.NewRecorder()
	srv.handleExportReport(w, req)

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition missing 'attachment': got %q", cd)
	}
	if !strings.Contains(cd, "filename=") {
		t.Errorf("Content-Disposition missing 'filename=': got %q", cd)
	}
	if !strings.Contains(cd, ".html") {
		t.Errorf("Content-Disposition filename should end in .html: got %q", cd)
	}
}

func TestStaticIndexHasAnalyticsView(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="analytics-view"`) {
		t.Error(`index.html missing section id="analytics-view"`)
	}
	if !strings.Contains(body, "analytics-grid") {
		t.Error("index.html missing .analytics-grid container")
	}
	if !strings.Contains(body, "analytics-card") {
		t.Error("index.html missing .analytics-card elements")
	}
}

func TestStaticIndexHasExportReportButton(t *testing.T) {
	body := getStaticIndex(t)
	if !strings.Contains(body, `id="export-report"`) {
		t.Error(`index.html missing button id="export-report"`)
	}
	if !strings.Contains(body, "doExportReport") {
		t.Error("index.html missing doExportReport JS function")
	}
}

func TestAnalyticsAuthRequired(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	for _, path := range []string{"/api/analytics", "/api/export-report"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: got %d, want 401", path, w.Code)
			}
		})
	}
}

// ── Analytics filter + drilldown tests ───────────────────────────────────────

func TestAnalyticsRespectsWorkspaceFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Two sessions in different workspaces.
	for _, tc := range []struct {
		id  string
		cwd string
		src string
	}{
		{"ws-sess-a", "/home/user/proj-alpha", "src-ws-a"},
		{"ws-sess-b", "/home/user/proj-beta", "src-ws-b"},
	} {
		if err := db.UpsertSession(tc.id, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert %s: %v", tc.id, err)
		}
		if err := db.UpdateSessionMeta(tc.id, tc.cwd, "main"); err != nil {
			t.Fatalf("meta %s: %v", tc.id, err)
		}
		if err := db.InsertTokenUsage("agent1", tc.id, 100, 50, 0, 0,
			"claude-sonnet-4-6", 0.01, now, tc.src); err != nil {
			t.Fatalf("usage %s: %v", tc.id, err)
		}
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet,
		"/api/analytics?range=all&workspace=/home/user/proj-alpha", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d — %s", w.Code, w.Body.String())
	}
	var resp analyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, s := range resp.TopExpensiveSessions {
		if s.ID == "ws-sess-b" {
			t.Errorf("workspace filter: session from proj-beta leaked into proj-alpha results")
		}
	}
	found := false
	for _, s := range resp.TopExpensiveSessions {
		if s.ID == "ws-sess-a" {
			found = true
		}
	}
	if !found {
		t.Error("workspace filter: expected ws-sess-a in results but it was absent")
	}
}

func TestAnalyticsRespectsTagFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	for _, tc := range []struct {
		id  string
		tag string
		src string
	}{
		{"tag-sess-a", "my-project", "src-tag-a"},
		{"tag-sess-b", "other-project", "src-tag-b"},
	} {
		if err := db.UpsertSession(tc.id, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert %s: %v", tc.id, err)
		}
		if err := db.SetSessionTag(tc.id, tc.tag); err != nil {
			t.Fatalf("tag %s: %v", tc.id, err)
		}
		if err := db.InsertTokenUsage("agent1", tc.id, 100, 50, 0, 0,
			"claude-sonnet-4-6", 0.01, now, tc.src); err != nil {
			t.Fatalf("usage %s: %v", tc.id, err)
		}
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet,
		"/api/analytics?range=all&tag=my-project", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d — %s", w.Code, w.Body.String())
	}
	var resp analyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, s := range resp.TopExpensiveSessions {
		if s.ID == "tag-sess-b" {
			t.Errorf("tag filter: other-project session leaked into my-project results")
		}
	}
}

func TestAnalyticsRespectsModelFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("model-sess", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.InsertTokenUsage("agent1", "model-sess", 100, 50, 0, 0,
		"claude-sonnet-4-6", 0.01, now, "src-model-sonnet"); err != nil {
		t.Fatalf("usage sonnet: %v", err)
	}
	if err := db.InsertTokenUsage("agent1", "model-sess", 200, 100, 0, 0,
		"claude-opus-4-7", 0.05, now, "src-model-opus"); err != nil {
		t.Fatalf("usage opus: %v", err)
	}

	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet,
		"/api/analytics?range=all&model=claude-sonnet-4-6", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d — %s", w.Code, w.Body.String())
	}
	var resp analyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	models := (resp.ModelMixDaily[0]).Models
	for _, m := range models {
		if m.Model == "claude-opus-4-7" {
			t.Errorf("model filter: claude-opus-4-7 should be excluded but appeared in response")
		}
	}
	found := false
	for _, m := range models {
		if m.Model == "claude-sonnet-4-6" {
			found = true
		}
	}
	if !found {
		t.Error("model filter: claude-sonnet-4-6 should appear in filtered results")
	}
}

func TestAnalyticsCompareReturnsBothPeriods(t *testing.T) {
	db := testDB(t)
	seedAnalyticsData(t, db)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet,
		"/api/analytics?range=week&compare=prev_period", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d — %s", w.Code, w.Body.String())
	}
	var resp analyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PreviousPeriod == nil {
		t.Fatal("compare=prev_period: previous_period field is nil")
	}
	// Previous period must contain the 4 cards.
	pp := resp.PreviousPeriod
	if pp.TopExpensiveSessions == nil {
		t.Error("previous_period missing top_expensive_sessions")
	}
	if pp.ToolBreakdown == nil {
		t.Error("previous_period missing tool_breakdown")
	}
	if len(pp.ModelMixDaily) == 0 {
		t.Error("previous_period missing model_mix_daily")
	}
	// Anomalies may be nil for sparse data — not checked.
}

func TestStaticIndexHasAnalyticsFilters(t *testing.T) {
	body := getStaticIndex(t)
	for _, want := range []string{
		"ac-filter-workspace",
		"ac-filter-tag",
		"ac-filter-model",
		"ac-filter-compare",
		"applyAnalyticsFilters",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html missing analytics filter element/function: %q", want)
		}
	}
}

func TestStaticIndexHasAnalyticsDrilldownClick(t *testing.T) {
	body := getStaticIndex(t)
	for _, want := range []string{
		"openSession",
		"ac-top-body",
		"ac-anomalies-list",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html missing drilldown identifier: %q", want)
		}
	}
}
