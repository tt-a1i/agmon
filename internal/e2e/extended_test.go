package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/web"
)

// startWebServerWithOpts starts a test web server accepting additional ServerOptions.
func startWebServerWithOpts(t *testing.T, db *storage.DB, sockPath string, opts ...web.ServerOption) string {
	t.Helper()
	port := freePort(t)
	allOpts := append([]web.ServerOption{web.WithEventSocketPath(sockPath)}, opts...)
	srv := web.NewServer(db, strconv.Itoa(port), allOpts...)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitFor(t, 2*time.Second, func() (bool, error) {
		select {
		case err := <-errCh:
			return false, fmt.Errorf("web server exited: %w", err)
		default:
		}
		resp, err := http.Get(baseURL + "/")
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		return resp.StatusCode < 500, nil
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-errCh
	})
	return baseURL
}

// seedSessionWithCWD creates a session with a specific CWD and token usage so it
// appears in workspace-filtered queries (which require tokens > 0 or active status).
func seedSessionWithCWD(t *testing.T, db *storage.DB, sessionID, cwd string) {
	t.Helper()
	ts := time.Now()
	if err := db.UpsertSession(sessionID, event.PlatformClaude, ts); err != nil {
		t.Fatalf("upsert session %s: %v", sessionID, err)
	}
	if err := db.UpdateSessionMeta(sessionID, cwd, "main"); err != nil {
		t.Fatalf("update meta %s: %v", sessionID, err)
	}
	if err := db.InsertTokenUsage("agent-"+sessionID, sessionID, 100, 50, 0, 0, "sonnet", 0.10, ts, "token-"+sessionID); err != nil {
		t.Fatalf("insert token usage %s: %v", sessionID, err)
	}
}

// doRequest executes an HTTP request with optional Authorization header.
func doRequest(t *testing.T, method, url, authHeader string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, url, err)
	}
	return resp
}

// TestE2EBackupRestoreRoundtrip verifies that BackupTo produces a valid SQLite
// file that contains the same sessions and cost totals as the source database.
func TestE2EBackupRestoreRoundtrip(t *testing.T) {
	rig := startTestDaemon(t)
	ts := time.Now()

	// Seed 3 sessions with known token usage.
	sessionIDs := []string{"backup-s1", "backup-s2", "backup-s3"}
	wantCosts := []float64{0.10, 0.25, 0.50}
	for i, sid := range sessionIDs {
		seedSession(t, rig.db, sid, event.PlatformClaude, ts.Add(time.Duration(i)*time.Minute), "sonnet", wantCosts[i])
	}

	// Take a backup.
	backupPath := t.TempDir() + "/backup.db"
	origSize, backupSize, err := rig.db.BackupTo(backupPath)
	if err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if origSize <= 0 || backupSize <= 0 {
		t.Fatalf("BackupTo returned zero sizes: orig=%d backup=%d", origSize, backupSize)
	}
	if _, statErr := os.Stat(backupPath); statErr != nil {
		t.Fatalf("backup file missing: %v", statErr)
	}

	// Validate that the backup is a readable SQLite database.
	if err := storage.ValidateSQLiteBackup(backupPath); err != nil {
		t.Fatalf("ValidateSQLiteBackup: %v", err)
	}

	// Open backup as a new DB and verify all sessions are present with correct costs.
	backupDB, err := storage.Open(backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	t.Cleanup(func() { _ = backupDB.Close() })

	sessions, err := backupDB.ListSessions()
	if err != nil {
		t.Fatalf("backup ListSessions: %v", err)
	}
	if len(sessions) != len(sessionIDs) {
		t.Fatalf("backup session count = %d, want %d", len(sessions), len(sessionIDs))
	}

	var totalCost float64
	for _, s := range sessions {
		totalCost += s.TotalCostUSD
	}
	const wantTotal = 0.10 + 0.25 + 0.50
	if totalCost < wantTotal-0.001 || totalCost > wantTotal+0.001 {
		t.Fatalf("backup total cost = %.4f, want %.4f", totalCost, wantTotal)
	}
}

// TestE2EWALCheckpoint verifies that CheckpointTruncate succeeds and reports
// a zero-length WAL file after truncation.
func TestE2EWALCheckpoint(t *testing.T) {
	rig := startTestDaemon(t)
	ts := time.Now()

	// Write enough data to create WAL entries.
	for i := range 20 {
		sid := fmt.Sprintf("wal-session-%02d", i)
		seedSession(t, rig.db, sid, event.PlatformClaude, ts.Add(time.Duration(i)*time.Second), "sonnet", 0.01)
	}

	result, err := rig.db.CheckpointTruncate()
	if err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}
	// After a successful TRUNCATE checkpoint, Busy should be 0.
	if result.Busy != 0 {
		t.Errorf("CheckpointTruncate busy = %d, want 0", result.Busy)
	}
	// WAL file should be absent or zero-length after TRUNCATE.
	if result.DBWalBytes != 0 {
		t.Errorf("WAL size after checkpoint = %d, want 0", result.DBWalBytes)
	}
}

// TestE2EWebBearerAuthRequired verifies that protected API endpoints require a
// valid Bearer token when the server is started with WithAuthToken.
func TestE2EWebBearerAuthRequired(t *testing.T) {
	const token = "supersecret-e2e-token"
	rig := startTestDaemon(t)
	baseURL := startWebServerWithOpts(t, rig.db, rig.sockPath, web.WithAuthToken(token))

	// No Authorization header → 401.
	resp := doRequest(t, http.MethodGet, baseURL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", resp.StatusCode)
	}

	// Wrong token → 401.
	resp2 := doRequest(t, http.MethodGet, baseURL+"/api/sessions", "Bearer wrong-token", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", resp2.StatusCode)
	}

	// Correct Bearer header → 200.
	resp3 := doRequest(t, http.MethodGet, baseURL+"/api/sessions", "Bearer "+token, nil)
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp3.Body)
		t.Errorf("correct token: status = %d, want 200; body=%s", resp3.StatusCode, body)
	}

	// ?token= URL param → connects SSE stream (200).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sseReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events?token="+token, nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("SSE with token param: %v", err)
	}
	if sseResp != nil {
		defer sseResp.Body.Close()
		if sseResp.StatusCode != http.StatusOK {
			t.Errorf("SSE ?token=: status = %d, want 200", sseResp.StatusCode)
		}
	}
}

// TestE2EWebBearerAuthDisabled verifies that when no token is configured,
// API endpoints are accessible without Authorization headers.
func TestE2EWebBearerAuthDisabled(t *testing.T) {
	rig := startTestDaemon(t)
	baseURL := startWebServerWithOpts(t, rig.db, rig.sockPath) // no WithAuthToken

	resp := doRequest(t, http.MethodGet, baseURL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("no auth configured: status = %d, want 200", resp.StatusCode)
	}
}

// TestE2EWebWorkspaceFilter verifies that GET /api/sessions?workspace= returns
// only sessions whose CWD matches the workspace path.
func TestE2EWebWorkspaceFilter(t *testing.T) {
	rig := startTestDaemon(t)
	baseURL := startWebServerWithOpts(t, rig.db, rig.sockPath)

	seedSessionWithCWD(t, rig.db, "ws-session-foo", "/code/foo")
	seedSessionWithCWD(t, rig.db, "ws-session-bar", "/code/bar")
	seedSessionWithCWD(t, rig.db, "ws-session-other", "/other/project")

	type sessionResp struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}

	// Filter by /code/foo — should return only the foo session.
	var fooSessions []sessionResp
	getJSON(t, baseURL+"/api/sessions?workspace=/code/foo", &fooSessions)
	if len(fooSessions) != 1 {
		t.Fatalf("workspace=/code/foo: got %d sessions, want 1; %+v", len(fooSessions), fooSessions)
	}
	if fooSessions[0].SessionID != "ws-session-foo" {
		t.Errorf("workspace=/code/foo: session_id = %s, want ws-session-foo", fooSessions[0].SessionID)
	}

	// Filter by /other/project — should return only other session.
	var otherSessions []sessionResp
	getJSON(t, baseURL+"/api/sessions?workspace=/other/project", &otherSessions)
	if len(otherSessions) != 1 {
		t.Fatalf("workspace=/other/project: got %d sessions, want 1; %+v", len(otherSessions), otherSessions)
	}
	if otherSessions[0].SessionID != "ws-session-other" {
		t.Errorf("workspace=/other: session_id = %s, want ws-session-other", otherSessions[0].SessionID)
	}

	// No workspace filter — should return all 3.
	var allSessions []sessionResp
	getJSON(t, baseURL+"/api/sessions", &allSessions)
	if len(allSessions) < 3 {
		t.Errorf("no workspace filter: got %d sessions, want ≥3", len(allSessions))
	}
}

// TestE2EWebTagFilter verifies that PATCH /api/session/{id}/tag sets a tag and
// the tag is returned in subsequent GET /api/sessions responses.
func TestE2EWebTagFilter(t *testing.T) {
	rig := startTestDaemon(t)
	baseURL := startWebServerWithOpts(t, rig.db, rig.sockPath)
	ts := time.Now()

	// Seed two sessions.
	seedSession(t, rig.db, "tag-session-urgent", event.PlatformClaude, ts, "sonnet", 0.10)
	seedSession(t, rig.db, "tag-session-plain", event.PlatformClaude, ts.Add(time.Minute), "sonnet", 0.05)

	// PUT to set tag on the first session (server expects PUT, not PATCH).
	tagBody, _ := json.Marshal(map[string]string{"tag": "urgent"})
	patchResp := doRequest(t, http.MethodPut, baseURL+"/api/session/tag-session-urgent/tag", "", bytes.NewReader(tagBody))
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(patchResp.Body)
		t.Fatalf("PUT tag: status = %d, body = %s", patchResp.StatusCode, body)
	}

	// Verify the tag is reflected in GET /api/sessions.
	type sessionResp struct {
		SessionID string `json:"session_id"`
		Tag       string `json:"tag"`
	}
	var sessions []sessionResp
	getJSON(t, baseURL+"/api/sessions", &sessions)

	var found bool
	for _, s := range sessions {
		if s.SessionID == "tag-session-urgent" {
			found = true
			if s.Tag != "urgent" {
				t.Errorf("session tag = %q, want %q", s.Tag, "urgent")
			}
		}
		if s.SessionID == "tag-session-plain" && s.Tag != "" {
			t.Errorf("plain session tag = %q, want empty", s.Tag)
		}
	}
	if !found {
		t.Errorf("tagged session not found in GET /api/sessions")
	}

	// PUT empty tag to clear it; verify tag is removed.
	clearBody, _ := json.Marshal(map[string]string{"tag": ""})
	clearResp := doRequest(t, http.MethodPut, baseURL+"/api/session/tag-session-urgent/tag", "", bytes.NewReader(clearBody))
	defer clearResp.Body.Close()
	if clearResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(clearResp.Body)
		t.Fatalf("PUT clear tag: status = %d, body = %s", clearResp.StatusCode, body)
	}

	var sessionsAfter []sessionResp
	getJSON(t, baseURL+"/api/sessions", &sessionsAfter)
	for _, s := range sessionsAfter {
		if s.SessionID == "tag-session-urgent" && s.Tag != "" {
			t.Errorf("after clear, tag = %q, want empty", s.Tag)
		}
	}
}

// TestE2EServiceWorkerHeaders verifies that sw.js and manifest.json are served
// with correct content-types and contain expected key strings.
func TestE2EServiceWorkerHeaders(t *testing.T) {
	rig := startTestDaemon(t)
	baseURL := startWebServerWithOpts(t, rig.db, rig.sockPath)

	// GET /sw.js
	swResp, err := http.Get(baseURL + "/sw.js")
	if err != nil {
		t.Fatalf("GET /sw.js: %v", err)
	}
	defer swResp.Body.Close()
	if swResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sw.js status = %d, want 200", swResp.StatusCode)
	}
	ct := swResp.Header.Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("sw.js Content-Type = %q, want application/javascript", ct)
	}
	swBody, err := io.ReadAll(swResp.Body)
	if err != nil {
		t.Fatalf("read sw.js: %v", err)
	}
	swContent := string(swBody)
	for _, want := range []string{"API_CACHE", "tokenmeter-static", "tokenmeter-api-v1"} {
		if !strings.Contains(swContent, want) {
			t.Errorf("sw.js missing %q", want)
		}
	}

	// GET /manifest.json
	mfResp, err := http.Get(baseURL + "/manifest.json")
	if err != nil {
		t.Fatalf("GET /manifest.json: %v", err)
	}
	defer mfResp.Body.Close()
	if mfResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /manifest.json status = %d, want 200", mfResp.StatusCode)
	}
	mfCT := mfResp.Header.Get("Content-Type")
	if !strings.Contains(mfCT, "json") {
		t.Errorf("manifest.json Content-Type = %q, want JSON", mfCT)
	}
	mfBody, err := io.ReadAll(mfResp.Body)
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(mfBody, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	for _, key := range []string{"name", "short_name", "icons"} {
		if _, ok := manifest[key]; !ok {
			t.Errorf("manifest.json missing field %q", key)
		}
	}
}
