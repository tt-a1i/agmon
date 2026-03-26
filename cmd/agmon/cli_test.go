package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

func withArgs(t *testing.T, args []string) {
	t.Helper()
	prev := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = prev })
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prev })

	fn()

	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(data)
}

func openHomeDB(t *testing.T, home string) *storage.DB {
	t.Helper()
	t.Setenv("HOME", home)
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readSettingsJSON(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	return settings
}

func TestRunSetupConfiguresClaudeHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := captureStdout(t, runSetup)
	if !strings.Contains(out, "Claude Code hooks configured") {
		t.Fatalf("unexpected stdout: %q", out)
	}

	settings := readSettingsJSON(t, home)
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("expected hooks object, got %#v", settings["hooks"])
	}
	for _, hookName := range agmonHookNames {
		if _, ok := hooks[hookName]; !ok {
			t.Fatalf("expected hook %q to be configured", hookName)
		}
	}
}

func TestRunUninstallRemovesOnlyAgmonHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "agmon emit"},
						map[string]any{"type": "command", "command": "custom-hook"},
					},
				},
			},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	out := captureStdout(t, runUninstall)
	if !strings.Contains(out, "Removed Claude Code hooks") {
		t.Fatalf("unexpected stdout: %q", out)
	}

	result := readSettingsJSON(t, home)
	hooks := result["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	entry := sessionStart[0].(map[string]any)
	innerHooks := entry["hooks"].([]any)
	if len(innerHooks) != 1 {
		t.Fatalf("expected only non-agmon hook to remain, got %#v", innerHooks)
	}
	got := innerHooks[0].(map[string]any)["command"].(string)
	if got != "custom-hook" {
		t.Fatalf("unexpected remaining hook: %q", got)
	}
}

func TestRunReportFindsHiddenSessionByID(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().UTC().Add(-2 * time.Hour)

	if err := db.UpsertSession("hidden-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.EndSession("hidden-session", now.Add(time.Minute)); err != nil {
		t.Fatalf("end session: %v", err)
	}

	withArgs(t, []string{"agmon", "report", "hidden-session"})
	out := captureStdout(t, runReport)

	if !strings.Contains(out, "ID:       hidden-session") {
		t.Fatalf("expected report to include hidden session id, got %q", out)
	}
	if !strings.Contains(out, "Status:   ended") {
		t.Fatalf("expected ended status in report, got %q", out)
	}
}

func TestRunCostOutputsTotalsForRequestedPeriod(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().UTC()

	if err := db.UpsertSession("cost-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent-1", "cost-session", 1200, 300, 0, 0, "sonnet", 2.5, now, "cost-src"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	if err := db.UpdateSessionTokens("cost-session"); err != nil {
		t.Fatalf("update session tokens: %v", err)
	}

	withArgs(t, []string{"agmon", "cost", "all"})
	out := captureStdout(t, runCost)

	if !strings.Contains(out, "All time:") {
		t.Fatalf("expected all-time label, got %q", out)
	}
	if !strings.Contains(out, "1.2k in") || !strings.Contains(out, "300 out") {
		t.Fatalf("expected token totals in output, got %q", out)
	}
	if !strings.Contains(out, "$2.5000") {
		t.Fatalf("expected cost output, got %q", out)
	}
}

func TestRunCleanRemovesOldSessions(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now().UTC()

	if err := db.UpsertSession("old-session", event.PlatformClaude, now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent-1", "old-session", 100, 50, 0, 0, "sonnet", 0.1, now.AddDate(0, 0, -10), "old-src"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	if err := db.UpdateSessionTokens("old-session"); err != nil {
		t.Fatalf("update session tokens: %v", err)
	}
	if err := db.EndSession("old-session", now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("end session: %v", err)
	}

	withArgs(t, []string{"agmon", "clean", "7"})
	out := captureStdout(t, runClean)
	if !strings.Contains(out, "Removed 1 session(s) older than 7 days.") {
		t.Fatalf("unexpected stdout: %q", out)
	}

	session, found, err := db.GetSessionByIDPrefix("old-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if found || session.SessionID != "" {
		t.Fatalf("expected old session to be removed, got %#v", session)
	}
}

func TestRunSetupPreservesExistingSettingsShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	original := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal original settings: %v", err)
	}
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	_ = captureStdout(t, runSetup)

	result := readSettingsJSON(t, home)
	if result["theme"] != "dark" {
		t.Fatalf("expected non-hook settings to be preserved, got %#v", result)
	}

	buf, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings bytes: %v", err)
	}
	if !bytes.Contains(buf, []byte(`"hooks"`)) {
		t.Fatalf("expected settings file to contain hooks section, got %q", string(buf))
	}
}

func TestRunEmitWithReaderReturnsParseError(t *testing.T) {
	err := runEmitWithReader("/tmp/does-not-matter.sock", strings.NewReader("{bad-json"))
	if err == nil {
		t.Fatal("expected invalid hook input to return an error")
	}
	if !strings.Contains(err.Error(), "decode hook stdin") {
		t.Fatalf("unexpected error: %v", err)
	}
}
