package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunHealthcheckExit0(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	writeHealthcheckPID(t, home, os.Getpid())

	var out bytes.Buffer
	code, err := runHealthcheckWithDeps(nil, &out, "")
	if err != nil {
		t.Fatalf("healthcheck: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%d, want 0; out=%s", code, out.String())
	}
	if strings.TrimSpace(out.String()) != "OK" {
		t.Fatalf("output = %q, want OK", out.String())
	}
}

func TestRunHealthcheckExit1(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	dbPath := storage.DefaultDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeHealthcheckPID(t, home, os.Getpid())

	var out bytes.Buffer
	code, err := runHealthcheckWithDeps(nil, &out, "")
	if err != nil {
		t.Fatalf("healthcheck damaged db: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%d, want 1; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "UNHEALTHY:") || !strings.Contains(out.String(), "db") {
		t.Fatalf("output = %q, want db unhealthy", out.String())
	}
}

func TestRunHealthcheckJSON(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	writeHealthcheckPID(t, home, os.Getpid())

	var out bytes.Buffer
	code, err := runHealthcheckWithDeps([]string{"--json"}, &out, "")
	if err != nil {
		t.Fatalf("healthcheck json: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%d, want 0; out=%s", code, out.String())
	}
	var resp healthcheckResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("healthcheck json invalid: %v\n%s", err, out.String())
	}
	if resp.Status != "healthy" || resp.Checks.DB.Status != "ok" {
		t.Fatalf("healthcheck json = %#v", resp)
	}
}

func writeHealthcheckPID(t *testing.T, home string, pid int) {
	t.Helper()
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "daemon.pid"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}
