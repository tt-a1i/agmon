package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tt-a1i/tokenmeter/internal/collector"
)

func setReloadTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, ".tokenmeter")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(base, "pricing.json"))
		collector.LoadPricingOverrides()
	})
	return base
}

func writeReloadFile(t *testing.T, base, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadConfigReloadsWebhooks(t *testing.T) {
	base := setReloadTestHome(t)
	writeReloadFile(t, base, "webhooks.json", `{"endpoints":[{"url":"https://example.com/one","events":["budget_warn"],"format":"json"}]}`)
	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))

	d.ReloadConfig()
	cfg := d.webhookConfig()
	if cfg == nil || len(cfg.Endpoints) != 1 || cfg.Endpoints[0].URL != "https://example.com/one" {
		t.Fatalf("initial webhooks = %#v", cfg)
	}

	writeReloadFile(t, base, "webhooks.json", `{"endpoints":[{"url":"https://example.com/two","events":["budget_over"],"format":"slack"},{"url":"https://example.com/three","events":["*"],"format":"discord"}]}`)
	d.ReloadConfig()

	cfg = d.webhookConfig()
	if cfg == nil || len(cfg.Endpoints) != 2 {
		t.Fatalf("reloaded webhooks = %#v, want 2 endpoints", cfg)
	}
	if cfg.Endpoints[0].URL != "https://example.com/two" || cfg.Endpoints[1].Format != "discord" {
		t.Fatalf("reloaded webhooks = %#v", cfg)
	}
}

func TestReloadConfigGracefulOnMalformedFile(t *testing.T) {
	base := setReloadTestHome(t)
	writeReloadFile(t, base, "webhooks.json", `{"endpoints":[{"url":"https://example.com/valid","events":["*"],"format":"json"}]}`)
	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))
	d.ReloadConfig()

	writeReloadFile(t, base, "webhooks.json", `{"endpoints":[`)
	d.ReloadConfig()

	cfg := d.webhookConfig()
	if cfg == nil || len(cfg.Endpoints) != 1 || cfg.Endpoints[0].URL != "https://example.com/valid" {
		t.Fatalf("malformed reload should keep previous webhooks, got %#v", cfg)
	}
}

func TestReloadPricingTriggers(t *testing.T) {
	base := setReloadTestHome(t)
	writeReloadFile(t, base, "pricing.json", `{
		"codex": [
			{"match": ["hotreload-model"], "inputPerMillion": 0.12, "outputPerMillion": 0.34, "cacheReadPerMill": 0.05}
		]
	}`)
	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))

	d.ReloadConfig()

	input, output, cache := collector.CodexPricing("hotreload-model")
	if input != 0.12 || output != 0.34 || cache != 0.05 {
		t.Fatalf("pricing reload not applied: input=%v output=%v cache=%v", input, output, cache)
	}
}
