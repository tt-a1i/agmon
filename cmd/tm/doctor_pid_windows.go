//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// daemonPIDRunning reads a PID file and verifies the process is alive
// via `tasklist`. Signal-based liveness probes don't work on Windows
// (os.Process.Signal returns "not supported" for any non-Kill signal),
// so doctor would otherwise always flag the daemon as stale on Windows.
func daemonPIDRunning(path string) (bool, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false, pid
	}
	if !strings.Contains(string(out), strconv.Itoa(pid)) {
		return false, pid
	}
	return true, pid
}
