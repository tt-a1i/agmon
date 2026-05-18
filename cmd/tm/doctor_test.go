package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func prepareDoctorCleanInstall(t *testing.T) *storage.DB {
	t.Helper()

	home, err := os.MkdirTemp("", "tmdoctor-")
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
	if err := os.MkdirAll(filepath.Join(home, ".tokenmeter", "backups"), 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
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

	if !strings.Contains(out, "[✗] Claude hooks missing tm emit") {
		t.Fatalf("expected missing hooks error:\n%s", out)
	}
	if !strings.Contains(out, "Run 'tm setup'") {
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

	if !strings.Contains(out, "[⚠] Database size") || !strings.Contains(out, "tm clean 30") {
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

func TestDoctorFixCreatesMissingBackupsDir(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	backupsDir := filepath.Join(home, ".tokenmeter", "backups")
	if err := os.RemoveAll(backupsDir); err != nil {
		t.Fatalf("remove backups dir: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if _, err := os.Stat(backupsDir); err != nil {
		t.Fatalf("expected backups dir created: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Backups dir") || !strings.Contains(out, "[✗→✓]") {
		t.Fatalf("expected fixed backups output:\n%s", out)
	}
}

func TestDoctorFixRemovesStalePidFile(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	pidPath := filepath.Join(home, ".tokenmeter", "daemon.pid")
	if err := os.WriteFile(pidPath, []byte("999999"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale pid removed, stat err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "Stale daemon.pid") || !strings.Contains(out, "[✗→✓]") {
		t.Fatalf("expected fixed stale pid output:\n%s", out)
	}
}

func TestDoctorFixChmodsSocketTo0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket mode does not apply on windows")
	}
	home := t.TempDir()
	openHomeDB(t, home)
	socketPath := daemon.DefaultSocketPath()
	if err := os.WriteFile(socketPath, []byte("socket placeholder"), 0o644); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %04o, want 0600\n%s", got, out)
	}
	if !strings.Contains(out, "fixed to 0600") || !strings.Contains(out, "[✗→✓]") {
		t.Fatalf("expected fixed socket output:\n%s", out)
	}
}

func TestDoctorFixDoesNotTouchRunningDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket mode does not apply on windows")
	}
	db := prepareDoctorCleanInstall(t)
	socketPath := daemon.DefaultSocketPath()
	if err := os.Chmod(socketPath, 0o644); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("running daemon socket mode changed to %04o\n%s", got, out)
	}
	_ = db
}

func TestDoctorFixReportsActionsInOutput(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	if !strings.Contains(out, "[✗→✓]") {
		t.Fatalf("expected fixed marker:\n%s", out)
	}
	if !strings.Contains(out, "Fixed:") || !strings.Contains(out, "Manual action needed:") {
		t.Fatalf("expected fix summary:\n%s", out)
	}
}

func TestDoctorFixJSON(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	backupsDir := filepath.Join(home, ".tokenmeter", "backups")
	if err := os.RemoveAll(backupsDir); err != nil {
		t.Fatalf("remove backups dir: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "doctor", "--fix", "--json"})

	out := captureStdout(t, func() {
		if err := runDoctor(); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	var payload struct {
		FixedCount int `json:"fixed_count"`
		Actions    []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("doctor --fix --json should be valid JSON: %v\n%s", err, out)
	}
	if payload.FixedCount == 0 {
		t.Fatalf("expected fixed_count > 0: %#v\n%s", payload, out)
	}
	if len(payload.Actions) == 0 {
		t.Fatalf("expected actions in json: %#v\n%s", payload, out)
	}
}
