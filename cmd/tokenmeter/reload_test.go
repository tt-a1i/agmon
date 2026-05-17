package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReloadMissingPidFile(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	err := runReload()
	if err == nil {
		t.Fatal("expected missing pid file error")
	}
	if !strings.Contains(err.Error(), "no running daemon (pid file missing)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunReloadInvalidPid(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "daemon.pid"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runReload()
	if err == nil {
		t.Fatal("expected invalid pid error")
	}
	if !strings.Contains(err.Error(), "invalid daemon pid") {
		t.Fatalf("unexpected error: %v", err)
	}
}
