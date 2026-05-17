package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestSessionHighCostTrigger(t *testing.T) {
	setWebhookTestHome(t)
	oldUnit := webhookRetryBackoffUnit
	webhookRetryBackoffUnit = time.Millisecond
	t.Cleanup(func() { webhookRetryBackoffUnit = oldUnit })

	gotCh := make(chan WebhookPayload, 1)
	srv := captureWebhookPayloadServer(t, gotCh)
	d := startWebhookRetryTestDaemon(t)
	d.setWebhookConfig(&WebhookConfig{Endpoints: []EndpointConfig{{
		URL: srv.URL, Format: "json", Events: []string{webhookEventSessionHighCost},
		Thresholds: WebhookThresholds{SessionHighCostUSD: 1},
		Retry:      RetryPolicy{MaxAttempts: 1},
	}}})

	now := time.Now().UTC()
	if err := d.processEvent(event.Event{ID: "high-cost-start", Type: event.EventSessionStart, SessionID: "high-cost", Platform: event.PlatformClaude, Timestamp: now}); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := d.processEvent(event.Event{
		ID: "high-cost-token", Type: event.EventTokenUsage, SessionID: "high-cost", AgentID: "agent",
		Platform: event.PlatformClaude, Timestamp: now.Add(time.Second),
		Data: event.EventData{InputTokens: 1, OutputTokens: 1, Model: "sonnet", CostUSD: 6},
	}); err != nil {
		t.Fatalf("token usage: %v", err)
	}
	if err := d.processEvent(event.Event{ID: "high-cost-end", Type: event.EventSessionEnd, SessionID: "high-cost", Platform: event.PlatformClaude, Timestamp: now.Add(2 * time.Second)}); err != nil {
		t.Fatalf("session end: %v", err)
	}

	select {
	case got := <-gotCh:
		if got.Event != webhookEventSessionHighCost || got.Session == nil || got.Session.SessionID != "high-cost" || got.Session.CostUSD != 6 {
			t.Fatalf("session high cost payload = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session_high_cost webhook")
	}
}

func TestToolFailureRateTrigger(t *testing.T) {
	setWebhookTestHome(t)
	oldUnit := webhookRetryBackoffUnit
	webhookRetryBackoffUnit = time.Millisecond
	t.Cleanup(func() { webhookRetryBackoffUnit = oldUnit })

	gotCh := make(chan WebhookPayload, 1)
	srv := captureWebhookPayloadServer(t, gotCh)
	d := startWebhookRetryTestDaemon(t)
	d.setWebhookConfig(&WebhookConfig{Endpoints: []EndpointConfig{{
		URL: srv.URL, Format: "json", Events: []string{webhookEventToolFailureRate},
		Thresholds: WebhookThresholds{ToolFailureRatePct: 20},
		Retry:      RetryPolicy{MaxAttempts: 1},
	}}})

	now := time.Now().UTC()
	if err := d.db.UpsertSession("tool-fail-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := d.db.InsertTokenUsage("agent", "tool-fail-session", 1, 1, 0, 0, "sonnet", 0.01, now, "tool-fail-token"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	for i := 0; i < 100; i++ {
		status := event.StatusSuccess
		if i < 25 {
			status = event.StatusFail
		}
		callID := "tool-fail-" + strconv.Itoa(i)
		start := now.Add(time.Duration(i) * time.Millisecond)
		if _, err := d.db.InsertToolCallStart(callID, "agent", "tool-fail-session", "Bash", "{}", start); err != nil {
			t.Fatalf("insert tool start %d: %v", i, err)
		}
		if err := d.db.UpdateToolCallEnd(callID, "result", status, 10, start.Add(time.Millisecond)); err != nil {
			t.Fatalf("insert tool end %d: %v", i, err)
		}
	}

	if err := d.checkToolFailureRates(context.Background()); err != nil {
		t.Fatalf("check tool failure rates: %v", err)
	}

	select {
	case got := <-gotCh:
		if got.Event != webhookEventToolFailureRate || got.Tool == nil || got.Tool.ToolName != "Bash" || got.Tool.FailCount != 25 || got.Tool.FailureRatePct != 25 {
			t.Fatalf("tool failure payload = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool_failure_rate webhook")
	}
}

func captureWebhookPayloadServer(t *testing.T, gotCh chan<- WebhookPayload) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var got WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			return
		}
		gotCh <- got
	}))
	t.Cleanup(srv.Close)
	return srv
}
