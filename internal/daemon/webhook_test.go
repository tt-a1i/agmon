package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func setWebhookTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	return filepath.Join(home, ".tokenmeter")
}

func writeWebhookConfig(t *testing.T, body string) {
	t.Helper()
	base := setWebhookTestHome(t)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "webhooks.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWebhookConfigAbsentFile(t *testing.T) {
	setWebhookTestHome(t)

	cfg, err := LoadWebhookConfig()
	if err != nil {
		t.Fatalf("LoadWebhookConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("config = %#v, want nil", cfg)
	}
}

func TestLoadWebhookConfigMalformedJSON(t *testing.T) {
	writeWebhookConfig(t, `{"endpoints": [`)

	cfg, err := LoadWebhookConfig()
	if err == nil {
		t.Fatal("LoadWebhookConfig malformed JSON returned nil error")
	}
	if cfg != nil {
		t.Fatalf("config = %#v, want nil", cfg)
	}
}

func TestPostWebhookSlackFormat(t *testing.T) {
	var got map[string]string
	srv := captureWebhookServer(t, &got)

	payload := BudgetWebhookPayload{
		Budget: BudgetWebhookBudget{Name: "Claude", Used: 85, Limit: 100, Percent: 85, Status: "warn"},
	}
	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "slack", Events: []string{"budget_warn"},
	}, "budget_warn", payload)
	if err != nil {
		t.Fatalf("PostWebhook: %v", err)
	}
	if got["text"] == "" || !strings.Contains(got["text"], "budget 'Claude' has warn") {
		t.Fatalf("slack payload = %#v", got)
	}
}

func TestPostWebhookDiscordFormat(t *testing.T) {
	var got map[string]string
	srv := captureWebhookServer(t, &got)

	payload := BudgetWebhookPayload{
		Budget: BudgetWebhookBudget{Name: "All", Used: 110, Limit: 100, Percent: 110, Status: "over"},
	}
	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "discord", Events: []string{"budget_over"},
	}, "budget_over", payload)
	if err != nil {
		t.Fatalf("PostWebhook: %v", err)
	}
	if got["content"] == "" || !strings.Contains(got["content"], "budget 'All' has over") {
		t.Fatalf("discord payload = %#v", got)
	}
}

func TestPostWebhookJSONFormat(t *testing.T) {
	var got BudgetWebhookPayload
	srv := captureWebhookServer(t, &got)

	payload := BudgetWebhookPayload{
		Event: "budget_over",
		Budget: BudgetWebhookBudget{
			Name: "Codex", Used: 125, Limit: 100, Percent: 125, Status: "over",
		},
		Timestamp: time.Now().UTC(),
	}
	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "json", Events: []string{"*"},
	}, "budget_over", payload)
	if err != nil {
		t.Fatalf("PostWebhook: %v", err)
	}
	if got.Event != "budget_over" || got.Budget.Name != "Codex" || got.Budget.Status != "over" {
		t.Fatalf("json payload = %#v", got)
	}
}

func TestPostWebhookEventsFilter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(srv.Close)

	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "json", Events: []string{"budget_over"},
	}, "budget_warn", BudgetWebhookPayload{})
	if err != nil {
		t.Fatalf("PostWebhook filtered event: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("filtered event posted %d requests, want 0", calls.Load())
	}
}

func TestPostWebhookWildcard(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(srv.Close)

	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "json", Events: []string{"*"},
	}, "budget_warn", BudgetWebhookPayload{Event: "budget_warn"})
	if err != nil {
		t.Fatalf("PostWebhook wildcard: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("wildcard posted %d requests, want 1", calls.Load())
	}
}

func TestPostWebhookTimeout(t *testing.T) {
	old := webhookHTTPClient
	webhookHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	t.Cleanup(func() { webhookHTTPClient = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	err := PostWebhook(context.Background(), EndpointConfig{
		URL: srv.URL, Format: "json", Events: []string{"budget_warn"},
	}, "budget_warn", BudgetWebhookPayload{Event: "budget_warn"})
	if err == nil {
		t.Fatal("PostWebhook timeout returned nil error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout took too long: %s", time.Since(start))
	}
}

func TestBudgetSweepDetectsTransition(t *testing.T) {
	db := webhookTestDB(t)
	now := time.Now().Add(-time.Minute)
	if err := db.UpsertSession("budget-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "budget-session", 1, 1, 0, 0, "sonnet", 10, now, "budget-ok"); err != nil {
		t.Fatalf("insert ok usage: %v", err)
	}
	budgetID, err := db.InsertBudget("Claude monthly", 100, "")
	if err != nil {
		t.Fatalf("insert budget: %v", err)
	}

	gotCh := make(chan BudgetWebhookPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var got BudgetWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		gotCh <- got
	}))
	t.Cleanup(srv.Close)
	d := New(db, filepath.Join(t.TempDir(), "daemon.sock"))
	d.webhooks = &WebhookConfig{Endpoints: []EndpointConfig{{
		URL: srv.URL, Format: "json", Events: []string{"budget_warn"},
	}}}
	d.budgetLastStatus = make(map[int64]string)

	if err := d.checkBudgetTransitions(context.Background()); err != nil {
		t.Fatalf("initial sweep: %v", err)
	}
	select {
	case got := <-gotCh:
		t.Fatalf("initial ok state should not post, got %#v", got)
	default:
	}

	if err := db.InsertTokenUsage("a1", "budget-session", 1, 1, 0, 0, "sonnet", 75, now.Add(time.Second), "budget-warn"); err != nil {
		t.Fatalf("insert warn usage: %v", err)
	}
	if err := d.checkBudgetTransitions(context.Background()); err != nil {
		t.Fatalf("transition sweep: %v", err)
	}
	var got BudgetWebhookPayload
	select {
	case got = <-gotCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook")
	}
	if got.Event != "budget_warn" || got.Budget.ID != budgetID || got.Budget.Status != "warn" {
		t.Fatalf("transition payload = %#v", got)
	}
}

func webhookTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "webhook.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func captureWebhookServer[T any](t *testing.T, got *T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(got); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
			t.Errorf("decode body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
