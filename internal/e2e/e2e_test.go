package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/web"
)

func TestE2EDaemonEmitFlow(t *testing.T) {
	rig := startTestDaemon(t)
	now := time.Now()
	sessionID := "e2e-daemon-flow"

	emitEvent(t, rig.sockPath, event.Event{
		ID:        "start-" + sessionID,
		Type:      event.EventSessionStart,
		SessionID: sessionID,
		AgentID:   "agent-flow",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data: event.EventData{
			CWD:       "/tmp/e2e-daemon-flow",
			GitBranch: "main",
		},
	})
	emitEvent(t, rig.sockPath, tokenEvent("usage-"+sessionID, sessionID, "agent-flow", 0.42, now.Add(10*time.Millisecond)))
	waitForSessionCost(t, rig.db, sessionID, 0.42, 2*time.Second)

	baseURL := startTestWebServer(t, rig.db, rig.sockPath)
	var sessions []struct {
		SessionID string  `json:"session_id"`
		CostUSD   float64 `json:"cost_usd"`
	}
	getJSON(t, baseURL+"/api/sessions", &sessions)
	if !sessionInList(sessions, sessionID) {
		t.Fatalf("/api/sessions missing %s: %#v", sessionID, sessions)
	}

	var stats struct {
		TodayCost float64 `json:"today_cost"`
	}
	getJSON(t, baseURL+"/api/stats", &stats)
	if stats.TodayCost < 0.42 {
		t.Fatalf("/api/stats today_cost = %.4f, want at least 0.42", stats.TodayCost)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new sse request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/events status = %d", resp.StatusCode)
	}
	lines := scanLines(resp.Body)
	waitForLine(t, lines, ": heartbeat", 2*time.Second)

	eventID := "sse-tool-" + sessionID
	emitEvent(t, rig.sockPath, event.Event{
		ID:        eventID,
		Type:      event.EventToolCallStart,
		SessionID: sessionID,
		AgentID:   "agent-flow",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
		Data:      event.EventData{ToolName: "Read", ToolParams: "README.md"},
	})
	dataLine := waitForLine(t, lines, "data:", 2*time.Second)
	if !strings.Contains(dataLine, eventID) || !strings.Contains(dataLine, string(event.EventToolCallStart)) {
		t.Fatalf("SSE data line = %q, want event %s", dataLine, eventID)
	}
}

func TestE2EBudgetTriggersWebhook(t *testing.T) {
	gotCh := make(chan daemon.BudgetWebhookPayload, 1)
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload daemon.BudgetWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotCh <- payload
	}))
	t.Cleanup(webhookServer.Close)

	rig := startTestDaemon(t)
	writeWebhookConfig(t, webhookServer.URL)
	rig.daemon.ReloadConfig()
	baseURL := startTestWebServer(t, rig.db, rig.sockPath)

	var created struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Usage struct {
			Status string `json:"status"`
		} `json:"usage"`
	}
	postJSON(t, baseURL+"/api/budgets", map[string]any{
		"name":        "E2E tiny budget",
		"monthly_usd": 0.01,
		"platform":    "",
	}, http.StatusCreated, &created)
	if created.ID == 0 || created.Usage.Status != "ok" {
		t.Fatalf("created budget = %#v", created)
	}

	if err := rig.daemon.RunBudgetSweepForTest(context.Background()); err != nil {
		t.Fatalf("baseline budget sweep: %v", err)
	}
	sessionID := "e2e-budget-over"
	emitEvent(t, rig.sockPath, event.Event{
		ID:        "start-" + sessionID,
		Type:      event.EventSessionStart,
		SessionID: sessionID,
		AgentID:   "agent-budget",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
	})
	emitEvent(t, rig.sockPath, tokenEvent("usage-"+sessionID, sessionID, "agent-budget", 0.02, time.Now()))
	waitForSessionCost(t, rig.db, sessionID, 0.02, 2*time.Second)
	if err := rig.daemon.RunBudgetSweepForTest(context.Background()); err != nil {
		t.Fatalf("transition budget sweep: %v", err)
	}

	select {
	case got := <-gotCh:
		if got.Event != "budget_over" || got.Budget.ID != created.ID || got.Budget.Status != "over" {
			t.Fatalf("webhook payload = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for budget webhook")
	}
}

func TestE2EExportRoundTrip(t *testing.T) {
	rig := startTestDaemon(t)
	now := time.Now()
	seedSession(t, rig.db, "e2e-export-session", event.PlatformClaude, now, "sonnet", 0.75)
	baseURL := startTestWebServer(t, rig.db, rig.sockPath)

	resp, body := getBody(t, baseURL+"/api/export?range=week&format=csv")
	if resp.Header.Get("Content-Type") == "" || !strings.Contains(resp.Header.Get("Content-Disposition"), "tokenmeter-week-") {
		t.Fatalf("unexpected export headers: content-type=%q disposition=%q", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("csv records = %#v, want header + row", records)
	}
	row := findCSVRow(records, "e2e-export-session")
	if row == nil {
		t.Fatalf("csv missing e2e-export-session: %#v", records)
	}
	cost, err := strconv.ParseFloat(row[8], 64)
	if err != nil {
		t.Fatalf("parse cost %q: %v", row[8], err)
	}
	if math.Abs(cost-0.75) > 0.000001 {
		t.Fatalf("export cost = %.6f, want 0.750000", cost)
	}
}

func TestE2ESearchEndToEnd(t *testing.T) {
	rig := startTestDaemon(t)
	sessionID := "e2e-search-session"
	now := time.Now()
	emitEvent(t, rig.sockPath, event.Event{
		ID:        "start-" + sessionID,
		Type:      event.EventSessionStart,
		SessionID: sessionID,
		AgentID:   "agent-search",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{CWD: "/tmp/e2e-search", GitBranch: "search-branch"},
	})
	emitEvent(t, rig.sockPath, event.Event{
		ID:        "call-" + sessionID,
		Type:      event.EventToolCallStart,
		SessionID: sessionID,
		AgentID:   "agent-search",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(time.Millisecond),
		Data:      event.EventData{ToolName: "Edit", ToolParams: "Edit foo file_foo.go"},
	})
	emitEvent(t, rig.sockPath, event.Event{
		ID:        "file-" + sessionID,
		Type:      event.EventFileChange,
		SessionID: sessionID,
		AgentID:   "agent-search",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(2 * time.Millisecond),
		Data:      event.EventData{FilePath: "/tmp/file_foo.go", ChangeType: event.FileEdit},
	})
	waitForSearchHit(t, rig.db, "foo", sessionID, 2*time.Second)
	baseURL := startTestWebServer(t, rig.db, rig.sockPath)

	var hits []storage.SearchHit
	getJSON(t, baseURL+"/api/search?q=foo", &hits)
	kinds := hitKindsForSession(hits, sessionID)
	if !kinds["tool_param"] || !kinds["file"] {
		t.Fatalf("search hits for %s kinds = %#v, all hits = %#v", sessionID, kinds, hits)
	}
}

func TestE2ECompareEndToEnd(t *testing.T) {
	rig := startTestDaemon(t)
	now := time.Now()
	seedComparisonSession(t, rig.db, "e2e-compare-a", event.PlatformClaude, now, 1.25, []string{"Read", "Edit"}, []string{"a_only.go", "common.go"})
	seedComparisonSession(t, rig.db, "e2e-compare-b", event.PlatformClaude, now.Add(time.Second), 2.50, []string{"Read", "Bash", "Bash"}, []string{"b_only.go", "common.go"})
	baseURL := startTestWebServer(t, rig.db, rig.sockPath)

	var got struct {
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
	getJSON(t, baseURL+"/api/compare?a=e2e-compare-a&b=e2e-compare-b", &got)
	if math.Abs(got.CostDiff.Delta-1.25) > 0.000001 {
		t.Fatalf("cost_diff = %#v, want delta 1.25", got.CostDiff)
	}
	toolCounts := map[string][2]int{}
	for _, row := range got.ToolDiff {
		toolCounts[row.Name] = [2]int{row.ACount, row.BCount}
	}
	if toolCounts["Read"] != [2]int{1, 1} || toolCounts["Bash"] != [2]int{0, 2} {
		t.Fatalf("tool diff = %#v", got.ToolDiff)
	}
	if !containsString(got.FileDiff.AOnly, "a_only.go") || !containsString(got.FileDiff.BOnly, "b_only.go") || !containsString(got.FileDiff.Common, "common.go") {
		t.Fatalf("file diff = %#v", got.FileDiff)
	}
}

func TestE2EMetricsSnapshot(t *testing.T) {
	rig := startTestDaemon(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := rig.db.InsertBudget(fmt.Sprintf("E2E Budget %d", i), 10, ""); err != nil {
			t.Fatalf("insert budget %d: %v", i, err)
		}
	}
	for i := 0; i < 100; i++ {
		sessionID := fmt.Sprintf("e2e-metrics-session-%03d", i)
		if err := rig.db.UpsertSession(sessionID, event.PlatformClaude, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("upsert session %s: %v", sessionID, err)
		}
	}
	for i := 0; i < 1000; i++ {
		sessionID := fmt.Sprintf("e2e-metrics-session-%03d", i%100)
		if err := rig.db.InsertTokenUsage("agent-metrics", sessionID, 10, 5, 0, 0, "sonnet", 0.001, now.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("metrics-token-%04d", i)); err != nil {
			t.Fatalf("insert token usage %d: %v", i, err)
		}
	}
	baseURL := startTestWebServer(t, rig.db, rig.sockPath)
	_, body := getBody(t, baseURL+"/metrics")
	text := string(body)
	for _, want := range []string{
		"tokenmeter_sessions_total 100",
		"tokenmeter_today_cost_usd",
		"tokenmeter_budget_used_usd",
		"tokenmeter_budget_percent",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/metrics missing %q:\n%s", want, text)
		}
	}
}

type testRig struct {
	daemon   *daemon.Daemon
	db       *storage.DB
	sockPath string
}

func startTestDaemon(t *testing.T) testRig {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	db, err := storage.Open(filepath.Join(root, "tokenmeter.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// macOS Unix-domain socket paths are capped at 104 bytes; t.TempDir paths
	// under /var/folders can exceed that once the subscriber suffix is added.
	sockDir, err := os.MkdirTemp("", "tm-e2e-sock-")
	if err != nil {
		t.Fatalf("create socket temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "d.sock")
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(d.Stop)
	return testRig{daemon: d, db: db, sockPath: sockPath}
}

func startTestWebServer(t *testing.T, db *storage.DB, sockPath string) string {
	t.Helper()
	port := freePort(t)
	srv := web.NewServer(db, strconv.Itoa(port), web.WithEventSocketPath(sockPath))
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitFor(t, 2*time.Second, func() (bool, error) {
		select {
		case err := <-errCh:
			return false, fmt.Errorf("web server exited: %w", err)
		default:
		}
		resp, err := http.Get(baseURL + "/api/sessions")
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown web server: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("web server returned: %v", err)
			}
		case <-time.After(time.Second):
			t.Errorf("timed out waiting for web server shutdown")
		}
	})
	return baseURL
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func emitEvent(t *testing.T, sockPath string, ev event.Event) {
	t.Helper()
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("%s-%s-%d", ev.Type, ev.SessionID, time.Now().UnixNano())
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if err := collector.EmitEvent(sockPath, ev); err != nil {
		t.Fatalf("emit event %s: %v", ev.ID, err)
	}
}

func tokenEvent(id, sessionID, agentID string, cost float64, ts time.Time) event.Event {
	return event.Event{
		ID:        id,
		Type:      event.EventTokenUsage,
		SessionID: sessionID,
		AgentID:   agentID,
		Platform:  event.PlatformClaude,
		Timestamp: ts,
		Data: event.EventData{
			InputTokens:  1000,
			OutputTokens: 500,
			Model:        "sonnet",
			CostUSD:      cost,
		},
	}
}

func seedSession(t *testing.T, db *storage.DB, sessionID string, platform event.Platform, ts time.Time, model string, cost float64) {
	t.Helper()
	if err := db.UpsertSession(sessionID, platform, ts); err != nil {
		t.Fatalf("upsert session %s: %v", sessionID, err)
	}
	if err := db.UpdateSessionMeta(sessionID, "/tmp/"+sessionID, "branch-"+sessionID); err != nil {
		t.Fatalf("update meta %s: %v", sessionID, err)
	}
	if err := db.InsertTokenUsage("agent-"+sessionID, sessionID, 100, 50, 0, 0, model, cost, ts, "token-"+sessionID); err != nil {
		t.Fatalf("insert usage %s: %v", sessionID, err)
	}
}

func seedComparisonSession(t *testing.T, db *storage.DB, sessionID string, platform event.Platform, ts time.Time, cost float64, tools, files []string) {
	t.Helper()
	seedSession(t, db, sessionID, platform, ts, "sonnet", cost)
	for i, toolName := range tools {
		callID := fmt.Sprintf("%s-call-%d", sessionID, i)
		if _, err := db.InsertToolCallStart(callID, "agent-"+sessionID, sessionID, toolName, toolName+" params", ts.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("insert tool start %s: %v", callID, err)
		}
		if err := db.UpdateToolCallEnd(callID, "ok", event.StatusSuccess, 100, ts.Add(time.Duration(i+1)*time.Millisecond)); err != nil {
			t.Fatalf("update tool end %s: %v", callID, err)
		}
	}
	for i, filePath := range files {
		if err := db.InsertFileChange(sessionID, filePath, event.FileEdit, ts.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("insert file change %s: %v", filePath, err)
		}
	}
}

func writeWebhookConfig(t *testing.T, webhookURL string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	dir := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create webhook config dir: %v", err)
	}
	body := fmt.Sprintf(`{"endpoints":[{"url":%q,"events":["budget_warn","budget_over"],"format":"json"}]}`, webhookURL)
	if err := os.WriteFile(filepath.Join(dir, "webhooks.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write webhook config: %v", err)
	}
}

func waitForSessionCost(t *testing.T, db *storage.DB, sessionID string, wantMin float64, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, func() (bool, error) {
		sess, ok, err := db.GetSessionByIDPrefix(sessionID)
		if err != nil || !ok {
			return false, err
		}
		return sess.TotalCostUSD >= wantMin, nil
	})
}

func waitForSearchHit(t *testing.T, db *storage.DB, query, sessionID string, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, func() (bool, error) {
		hits, err := db.SearchHits(query, 20)
		if err != nil {
			return false, err
		}
		for _, hit := range hits {
			if hit.SessionID == sessionID {
				return true, nil
			}
		}
		return false, nil
	})
}

func waitFor(t *testing.T, timeout time.Duration, fn func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := fn()
		if ok {
			return
		}
		if err != nil {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("condition not met within %s: %v", timeout, lastErr)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func scanLines(r io.Reader) <-chan string {
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return lines
}

func waitForLine(t *testing.T, lines <-chan string, contains string, timeout time.Duration) string {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before line containing %q", contains)
			}
			if strings.Contains(line, contains) {
				return line
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for line containing %q", contains)
		}
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, body := getBody(t, url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d body = %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode GET %s: %v; body=%s", url, err, body)
	}
}

func postJSON(t *testing.T, url string, payload any, wantStatus int, out any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal post body: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s: %v", url, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d body=%s", url, resp.StatusCode, wantStatus, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			t.Fatalf("decode POST %s: %v body=%s", url, err, respBody)
		}
	}
}

func getBody(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d body=%s", url, resp.StatusCode, body)
	}
	return resp, body
}

func sessionInList(sessions []struct {
	SessionID string  `json:"session_id"`
	CostUSD   float64 `json:"cost_usd"`
}, sessionID string) bool {
	for _, sess := range sessions {
		if sess.SessionID == sessionID {
			return true
		}
	}
	return false
}

func findCSVRow(records [][]string, sessionID string) []string {
	for _, row := range records[1:] {
		if len(row) > 1 && row[1] == sessionID {
			return row
		}
	}
	return nil
}

func hitKindsForSession(hits []storage.SearchHit, sessionID string) map[string]bool {
	kinds := make(map[string]bool)
	for _, hit := range hits {
		if hit.SessionID == sessionID {
			kinds[hit.Kind] = true
		}
	}
	return kinds
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
