package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLogsPrintsTail(t *testing.T) {
	home := t.TempDir()
	writeLogLines(t, home, "daemon.log", 200)

	logsWithArgs(t, []string{"tokenmeter", "logs"})
	out := captureLogsStdout(t, func() {
		if err := runLogs(); err != nil {
			t.Fatalf("runLogs: %v", err)
		}
	})

	if strings.Contains(out, "line-099") {
		t.Fatalf("default logs should omit older lines:\n%s", out)
	}
	if !strings.Contains(out, "line-100") || !strings.Contains(out, "line-199") {
		t.Fatalf("default logs should print last 100 lines:\n%s", out)
	}
}

func TestRunLogsRespectsLinesFlag(t *testing.T) {
	home := t.TempDir()
	writeLogLines(t, home, "daemon.log", 200)

	logsWithArgs(t, []string{"tokenmeter", "logs", "--lines", "50"})
	out := captureLogsStdout(t, func() {
		if err := runLogs(); err != nil {
			t.Fatalf("runLogs: %v", err)
		}
	})

	if strings.Contains(out, "line-149") {
		t.Fatalf("logs --lines 50 should omit older lines:\n%s", out)
	}
	if !strings.Contains(out, "line-150") || !strings.Contains(out, "line-199") {
		t.Fatalf("logs --lines 50 should print last 50 lines:\n%s", out)
	}
}

func TestRunLogsEmitFlag(t *testing.T) {
	home := t.TempDir()
	writeLogFile(t, home, "daemon.log", "daemon-only\n")
	writeLogFile(t, home, "emit.log", "emit-only\n")

	logsWithArgs(t, []string{"tokenmeter", "logs", "--emit"})
	out := captureLogsStdout(t, func() {
		if err := runLogs(); err != nil {
			t.Fatalf("runLogs --emit: %v", err)
		}
	})

	if !strings.Contains(out, "emit-only") || strings.Contains(out, "daemon-only") {
		t.Fatalf("logs --emit read wrong file:\n%s", out)
	}
}

func TestRunLogsPathFlag(t *testing.T) {
	home := t.TempDir()
	setLogsTestHome(t, home)

	logsWithArgs(t, []string{"tokenmeter", "logs", "--path"})
	out := captureLogsStdout(t, func() {
		if err := runLogs(); err != nil {
			t.Fatalf("runLogs --path: %v", err)
		}
	})

	wantDaemon := filepath.Join(home, ".tokenmeter", "daemon.log")
	wantEmit := filepath.Join(home, ".tokenmeter", "emit.log")
	if !strings.Contains(out, wantDaemon) || !strings.Contains(out, wantEmit) {
		t.Fatalf("logs --path output = %q, want paths %q and %q", out, wantDaemon, wantEmit)
	}
}

func writeLogLines(t *testing.T, home, name string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line-")
		b.WriteString(pad3(i))
		b.WriteByte('\n')
	}
	writeLogFile(t, home, name, b.String())
}

func writeLogFile(t *testing.T, home, name, content string) {
	t.Helper()
	setLogsTestHome(t, home)
	path := filepath.Join(home, ".tokenmeter", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func logsWithArgs(t *testing.T, args []string) {
	t.Helper()
	prev := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = prev })
}

func setLogsTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}

func captureLogsStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prev })

	fn()

	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(data)
}

func pad3(n int) string {
	if n < 10 {
		return "00" + string(rune('0'+n))
	}
	if n < 100 {
		return "0" + string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}
