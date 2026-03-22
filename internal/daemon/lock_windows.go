//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func pidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agmon", "daemon.pid")
}

// WritePID writes the current process PID to the lock file.
func WritePID() error {
	path := pidFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

// RemovePID removes the PID lock file.
func RemovePID() {
	os.Remove(pidFilePath())
}

// IsRunning checks if another daemon instance is already running.
// On Windows, use tasklist to verify the PID is alive.
func IsRunning() (bool, int) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	// Use tasklist to check if PID exists
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		os.Remove(pidFilePath())
		return false, 0
	}

	if !strings.Contains(string(out), strconv.Itoa(pid)) {
		os.Remove(pidFilePath())
		return false, 0
	}

	return true, pid
}

// EnsureNotRunning returns an error if a daemon is already running.
func EnsureNotRunning() error {
	running, pid := IsRunning()
	if running {
		return fmt.Errorf("daemon already running (pid %d)", pid)
	}
	return nil
}
