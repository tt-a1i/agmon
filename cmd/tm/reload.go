package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

func runReload() error {
	if maybePrintCmdHelp("reload", os.Args[2:]) {
		return nil
	}
	path := appdir.Path("daemon.pid")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no running daemon (pid file missing)")
		}
		return fmt.Errorf("read daemon pid: %w", err)
	}
	raw := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid daemon pid %q", raw)
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stale pid file, daemon not running")
		}
		return fmt.Errorf("send SIGHUP to daemon (pid %d): %w", pid, err)
	}
	fmt.Printf("sent SIGHUP to daemon (pid %d)\n", pid)
	return nil
}
