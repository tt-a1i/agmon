package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebhookList(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "webhooks.json"), []byte(`{"endpoints":[{"url":"https://example.test/one","events":["budget_over"],"format":"slack"},{"url":"https://example.test/two","events":["*"],"format":"json"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	withArgs(t, []string{"tokenmeter", "webhook", "list"})
	out := captureStdout(t, func() {
		if err := runWebhook(); err != nil {
			t.Fatalf("runWebhook list: %v", err)
		}
	})

	for _, want := range []string{"https://example.test/one", "budget_over", "slack", "https://example.test/two", "*", "json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("webhook list output missing %q:\n%s", want, out)
		}
	}
}

func TestWebhookTest(t *testing.T) {
	gotCh := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode test webhook: %v", err)
			return
		}
		gotCh <- got
	}))
	t.Cleanup(srv.Close)

	withArgs(t, []string{"tokenmeter", "webhook", "test", srv.URL})
	out := captureStdout(t, func() {
		if err := runWebhook(); err != nil {
			t.Fatalf("runWebhook test: %v", err)
		}
	})
	if !strings.Contains(out, "Webhook test sent") {
		t.Fatalf("webhook test output:\n%s", out)
	}

	select {
	case got := <-gotCh:
		if got["event"] != "webhook_test" {
			t.Fatalf("test webhook payload = %#v", got)
		}
	default:
		t.Fatal("test webhook did not post")
	}
}
