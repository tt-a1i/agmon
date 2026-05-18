//go:build !windows

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// daemonPIDRunning reads a PID file and verifies the process is alive
// by sending signal 0 (no-op delivery, but errors if the PID is dead).
func daemonPIDRunning(path string) (bool, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, pid
	}
	return true, pid
}
