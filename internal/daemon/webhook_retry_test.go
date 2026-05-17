package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookRetryOnTransientFailure(t *testing.T) {
	setWebhookTestHome(t)
	oldUnit := webhookRetryBackoffUnit
	webhookRetryBackoffUnit = time.Millisecond
	t.Cleanup(func() { webhookRetryBackoffUnit = oldUnit })

	var calls atomic.Int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		close(done)
	}))
	t.Cleanup(srv.Close)

	d := startWebhookRetryTestDaemon(t)
	d.dispatchWebhookEvent(context.Background(), webhookEventDaemonStarted, WebhookPayload{
		Daemon: &DaemonWebhookPayload{Status: "started"},
	}, []EndpointConfig{{
		URL: srv.URL, Format: "json", Events: []string{webhookEventDaemonStarted},
		Retry: RetryPolicy{MaxAttempts: 2, InitialBackoffSeconds: 1},
	}})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for retry, calls=%d", calls.Load())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestWebhookRetryGivesUpAfterMaxAttempts(t *testing.T) {
	base := setWebhookTestHome(t)
	oldUnit := webhookRetryBackoffUnit
	webhookRetryBackoffUnit = time.Millisecond
	t.Cleanup(func() { webhookRetryBackoffUnit = oldUnit })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	d := startWebhookRetryTestDaemon(t)
	d.dispatchWebhookEvent(context.Background(), webhookEventDaemonStarted, WebhookPayload{
		Daemon: &DaemonWebhookPayload{Status: "started"},
	}, []EndpointConfig{{
		URL: srv.URL, Format: "json", Events: []string{webhookEventDaemonStarted},
		Retry: RetryPolicy{MaxAttempts: 2, InitialBackoffSeconds: 1},
	}})

	waitForWebhookTest(t, func() bool {
		data, err := os.ReadFile(filepath.Join(base, "webhooks-failed.log"))
		return calls.Load() == 2 && err == nil && len(data) > 0
	})
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestDeadLetterAppendsJSONLine(t *testing.T) {
	base := setWebhookTestHome(t)
	payload := WebhookPayload{
		Event:     webhookEventDaemonStarted,
		Timestamp: time.Now().UTC(),
		Daemon:    &DaemonWebhookPayload{Status: "started"},
	}
	if err := appendWebhookDeadLetter(EndpointConfig{URL: "https://example.test/hook", Format: "json"}, webhookEventDaemonStarted, payload, 3, "boom"); err != nil {
		t.Fatalf("append dead letter: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(base, "webhooks-failed.log"))
	if err != nil {
		t.Fatalf("read dead letter: %v", err)
	}
	lines := splitWebhookJSONLines(data)
	if len(lines) != 1 {
		t.Fatalf("dead letter lines=%d, want 1: %q", len(lines), string(data))
	}
	var got WebhookDeadLetter
	if err := json.Unmarshal(lines[0], &got); err != nil {
		t.Fatalf("unmarshal dead letter: %v", err)
	}
	if got.Event != webhookEventDaemonStarted || got.EndpointURL != "https://example.test/hook" || got.Attempts != 3 || got.Error != "boom" {
		t.Fatalf("dead letter = %#v", got)
	}
}

func startWebhookRetryTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))
	d.startWebhookRetryLoop()
	waitForWebhookTest(t, func() bool { return d.webhookRetryRunning.Load() })
	t.Cleanup(func() {
		close(d.done)
		d.bgWG.Wait()
	})
	return d
}

func waitForWebhookTest(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for webhook condition")
}

func splitWebhookJSONLines(data []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytesSplit(data, '\n') {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func bytesSplit(data []byte, sep byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == sep {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start <= len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
