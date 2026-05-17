//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// setHomeDir points appdir.Base() at a tmp directory by overriding HOME.
// Returns the corresponding ~/.tokenmeter dir so tests can inspect it.
func setHomeDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return filepath.Join(tmp, ".tokenmeter")
}

func TestWritePIDAndRemovePID(t *testing.T) {
	base := setHomeDir(t)

	if err := WritePID(); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	pidPath := filepath.Join(base, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid in file = %d, want %d", pid, os.Getpid())
	}

	RemovePID()
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("expected pid file removed, stat err = %v", err)
	}
}

func TestIsRunningWithLiveProcess(t *testing.T) {
	setHomeDir(t)
	if err := WritePID(); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	t.Cleanup(RemovePID)

	running, pid := IsRunning()
	if !running {
		t.Errorf("IsRunning should report this process as running")
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestIsRunningCleansStalePID(t *testing.T) {
	base := setHomeDir(t)
	pidPath := filepath.Join(base, "daemon.pid")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// PID 1 always exists, so pick a high unlikely-to-exist PID.
	if err := os.WriteFile(pidPath, []byte("99999999"), 0o644); err != nil {
		t.Fatalf("write fake pid: %v", err)
	}

	running, _ := IsRunning()
	if running {
		t.Errorf("expected stale PID to be reported not running")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("stale PID file should be cleaned up, stat err = %v", err)
	}
}

func TestEnsureNotRunningOK(t *testing.T) {
	setHomeDir(t)
	// No PID file present.
	if err := EnsureNotRunning(); err != nil {
		t.Errorf("EnsureNotRunning with no pid file: %v", err)
	}
}

func TestEnsureNotRunningWithLivePID(t *testing.T) {
	setHomeDir(t)
	if err := WritePID(); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	t.Cleanup(RemovePID)

	err := EnsureNotRunning()
	if err == nil {
		t.Errorf("expected error when our own pid file is present")
	}
}

func TestIsRunningHandlesGarbagePID(t *testing.T) {
	base := setHomeDir(t)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "daemon.pid"), []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write garbage pid: %v", err)
	}
	running, _ := IsRunning()
	if running {
		t.Errorf("expected garbage pid to be reported not running")
	}
}
