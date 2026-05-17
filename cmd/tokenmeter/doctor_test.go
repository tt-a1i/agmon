package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func prepareDoctorCleanInstall(t *testing.T) *storage.DB {
	t.Helper()

	home, err := os.MkdirTemp("/tmp", "tmdoctor-")
	if err != nil {
		t.Fatalf("create short temp home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	db := openHomeDB(t, home)
	now := time.Now().UTC()

	if err := db.UpsertSession("doctor-session", event.PlatformClaude, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertAgent("doctor-agent", "doctor-session", "", "main", now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := db.InsertToolCallStart("doctor-call", "doctor-agent", "doctor-session", "Edit", `{"file_path":"main.go"}`, now.Add(-90*time.Second)); err != nil {
		t.Fatalf("insert tool: %v", err)
	}
	if err := db.InsertTokenUsage("doctor-agent", "doctor-session", 1000, 250, 0, 0, "sonnet", 0.42, now.Add(-time.Minute), "doctor-token"); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	codexDir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir codex sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write codex session: %v", err)
	}

	_ = captureStdout(t, runSetup)

	d := daemon.New(db, daemon.DefaultSocketPath())
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	if err := daemon.WritePID(); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	t.Cleanup(func() {
		d.Stop()
		daemon.RemovePID()
	})

	return db
}

func TestDoctorWithCleanInstall(t *testing.T) {
	prepareDoctorCleanInstall(t)
	withArgs(t, []string{"tokenmeter", "doctor"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if !strings.Contains(out, "TokenMeter Doctor") {
		t.Fatalf("missing doctor header:\n%s", out)
	}
	if strings.Contains(out, "[✗]") || strings.Contains(out, "[⚠]") {
		t.Fatalf("clean install should be all ok:\n%s", out)
	}
	if got := strings.Count(out, "[✓]"); got < 15 {
		t.Fatalf("expected at least 15 ok checks, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "Summary:") || !strings.Contains(out, "0 warnings, 0 errors") {
		t.Fatalf("missing clean summary:\n%s", out)
	}
}

func TestDoctorDetectsMissingHooks(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "doctor"})
	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if !strings.Contains(out, "[✗] Claude hooks missing tokenmeter emit") {
		t.Fatalf("expected missing hooks error:\n%s", out)
	}
	if !strings.Contains(out, "Run 'tokenmeter setup'") {
		t.Fatalf("expected setup suggestion:\n%s", out)
	}
}

func TestDoctorDetectsLargeDB(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	prevWarn, prevErr := doctorDBWarnSizeBytes, doctorDBErrorSizeBytes
	doctorDBWarnSizeBytes = 1
	doctorDBErrorSizeBytes = 1 << 30
	t.Cleanup(func() {
		doctorDBWarnSizeBytes = prevWarn
		doctorDBErrorSizeBytes = prevErr
	})

	withArgs(t, []string{"tokenmeter", "doctor"})
	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if !strings.Contains(out, "[⚠] Database size") || !strings.Contains(out, "tokenmeter clean 30") {
		t.Fatalf("expected db size warning:\n%s", out)
	}
}

func TestDoctorJSONFormat(t *testing.T) {
	prepareDoctorCleanInstall(t)
	withArgs(t, []string{"tokenmeter", "doctor", "--json"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	var checks []doctorCheckResult
	if err := json.Unmarshal([]byte(out), &checks); err != nil {
		t.Fatalf("doctor json should be valid JSON: %v\n%s", err, out)
	}
	if len(checks) < 15 {
		t.Fatalf("expected at least 15 checks, got %d", len(checks))
	}
	if checks[0].Status != doctorStatusOK || checks[0].Message == "" {
		t.Fatalf("unexpected first check: %#v", checks[0])
	}
}

func TestDoctorDetectsCorruptDB(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	dbPath := storage.DefaultDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("not sqlite"), 0o644); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "doctor"})
	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if !strings.Contains(out, "[✗] Database") {
		t.Fatalf("expected corrupt db error:\n%s", out)
	}
	if !strings.Contains(out, "open") && !strings.Contains(out, "readable") {
		t.Fatalf("expected database error detail:\n%s", out)
	}
}

func TestDoctorDetectsMissingDB(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".tokenmeter"), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "doctor"})
	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	want := fmt.Sprintf("[✗] Database %s missing", storage.DefaultDBPath())
	if !strings.Contains(out, want) {
		t.Fatalf("expected missing db error %q:\n%s", want, out)
	}
}
