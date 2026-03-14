package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
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
func IsRunning() (bool, int) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// Check if process is actually running
	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		// Process not running, stale PID file
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
