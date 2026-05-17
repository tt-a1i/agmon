package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunWatchNoFilter(t *testing.T) {
	d, _, sockPath := startWatchDaemon(t)
	lines, stopCh, done := startWatch(t, nil, sockPath)
	defer stopWatch(t, stopCh, done)

	line := emitWatchEventUntilLine(t, d, lines, func(i int) event.Event {
		return event.Event{
			ID:        fmt.Sprintf("watch-all-%d", i),
			Type:      event.EventSessionStart,
			SessionID: "watch-all-session",
			Platform:  event.PlatformClaude,
			Timestamp: time.Now(),
		}
	})
	if !strings.Contains(line, "watch-al") || !strings.Contains(line, "claude") || !strings.Contains(line, "SessionStart") {
		t.Fatalf("unexpected watch output: %q", line)
	}
}

func TestRunWatchSessionFilter(t *testing.T) {
	d, db, sockPath := startWatchDaemon(t)
	now := time.Now()
	if err := db.UpsertSession("watch-keep-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert keep session: %v", err)
	}
	if err := db.UpsertSession("watch-drop-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert drop session: %v", err)
	}

	lines, stopCh, done := startWatch(t, []string{"watch-keep"}, sockPath)
	defer stopWatch(t, stopCh, done)

	line := emitWatchEventUntilLine(t, d, lines, func(i int) event.Event {
		if i%2 == 0 {
			return event.Event{
				ID:        fmt.Sprintf("watch-drop-%d", i),
				Type:      event.EventSessionStart,
				SessionID: "watch-drop-session",
				Platform:  event.PlatformClaude,
				Timestamp: now,
			}
		}
		return event.Event{
			ID:        fmt.Sprintf("watch-keep-%d", i),
			Type:      event.EventSessionStart,
			SessionID: "watch-keep-session",
			Platform:  event.PlatformClaude,
			Timestamp: now,
		}
	})
	if strings.Contains(line, "watch-dr") {
		t.Fatalf("session filter allowed dropped session: %q", line)
	}
	if !strings.Contains(line, "watch-ke") {
		t.Fatalf("session filter did not output selected session: %q", line)
	}
}

func TestRunWatchTypeFilter(t *testing.T) {
	d, db, sockPath := startWatchDaemon(t)
	now := time.Now()
	if err := db.UpsertSession("watch-token-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertAgent("agent-watch-token", "watch-token-session", "", "main", now); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	lines, stopCh, done := startWatch(t, []string{"--types", "token"}, sockPath)
	defer stopWatch(t, stopCh, done)

	line := emitWatchEventUntilLine(t, d, lines, func(i int) event.Event {
		if i%2 == 0 {
			return event.Event{
				ID:        fmt.Sprintf("watch-file-%d", i),
				Type:      event.EventFileChange,
				SessionID: "watch-token-session",
				Platform:  event.PlatformClaude,
				Timestamp: now,
				Data: event.EventData{
					FilePath:   "internal/watch.go",
					ChangeType: event.FileEdit,
				},
			}
		}
		return event.Event{
			ID:        fmt.Sprintf("watch-token-%d", i),
			Type:      event.EventTokenUsage,
			SessionID: "watch-token-session",
			AgentID:   "agent-watch-token",
			Platform:  event.PlatformClaude,
			Timestamp: now,
			Data: event.EventData{
				InputTokens:  42,
				OutputTokens: 7,
				Model:        "sonnet",
				CostUSD:      0.012,
			},
		}
	})
	if strings.Contains(line, "FileChange") {
		t.Fatalf("type filter allowed file event: %q", line)
	}
	if !strings.Contains(line, "TokenUsage") || !strings.Contains(line, "in=42 out=7 cost=$0.012") {
		t.Fatalf("type filter did not output token event: %q", line)
	}
}

func TestRunWatchNoColor(t *testing.T) {
	d, _, sockPath := startWatchDaemon(t)
	lines, stopCh, done := startWatch(t, []string{"--no-color"}, sockPath)
	defer stopWatch(t, stopCh, done)

	line := emitWatchEventUntilLine(t, d, lines, func(i int) event.Event {
		return event.Event{
			ID:        fmt.Sprintf("watch-nocolor-%d", i),
			Type:      event.EventFileChange,
			SessionID: "watch-nocolor-session",
			Platform:  event.PlatformCodex,
			Timestamp: time.Now(),
			Data: event.EventData{
				FilePath:   "src/new.go",
				ChangeType: event.FileCreate,
			},
		}
	})
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("--no-color output contained ANSI escape: %q", line)
	}
	if !strings.Contains(line, "FileChange") || !strings.Contains(line, "create src/new.go") {
		t.Fatalf("unexpected --no-color output: %q", line)
	}
}

func startWatchDaemon(t *testing.T) (*daemon.Daemon, *storage.DB, string) {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)

	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sockPath := fmt.Sprintf("%s/tokenmeter-watch-%d.sock", os.TempDir(), time.Now().UnixNano())
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(d.Stop)
	return d, db, sockPath
}

func startWatch(t *testing.T, args []string, sockPath string) (<-chan string, chan<- os.Signal, <-chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	ready := make(chan struct{})
	subscribe := func(path string) (<-chan event.Event, func(), error) {
		eventCh, closeFn, err := daemon.SubscribeRemote(path)
		if err == nil {
			close(ready)
		}
		return eventCh, closeFn, err
	}
	stopCh := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		defer pw.Close()
		done <- runWatchWithDeps(args, pw, io.Discard, sockPath, subscribe, stopCh)
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("watch exited before subscribing: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch subscription")
	}
	time.Sleep(25 * time.Millisecond)
	return lines, stopCh, done
}

func stopWatch(t *testing.T, stopCh chan<- os.Signal, done <-chan error) {
	t.Helper()
	stopCh <- syscall.SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out stopping watch")
	}
}

func emitWatchEventUntilLine(t *testing.T, d *daemon.Daemon, lines <-chan string, makeEvent func(int) event.Event) string {
	t.Helper()
	for i := 0; i < 10; i++ {
		d.ProcessExternalEvent(makeEvent(i))
		select {
		case line := <-lines:
			return line
		case <-time.After(150 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for watch output")
	return ""
}
