package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// TestExpandedMessageRendersInTinyTerminal regresses the infinite-loop in
// the message wrapping loop when terminal width is small enough to push
// displayTruncate into returning "..." (visible prefix length 0).
//
// Before the fix, the chunking loop would never advance and lock up
// bubbletea's render path. We assert that the call returns at all — if the
// loop is broken again, the test process times out.
func TestExpandedMessageRendersInTinyTerminal(t *testing.T) {
	db := newTestDB(t)

	m := NewModel(db, nil)
	m.sessions = []storage.SessionRow{{SessionID: "tiny", Platform: "claude", StartTime: time.Now()}}
	m.selectedSession = 0
	m.messages = []collector.UserMessage{
		{Timestamp: time.Now(), Content: "中文消息内容很长这里是测试"},
	}
	m.refreshFilteredViews()
	m.expandedCalls = map[string]bool{
		messageExpansionKeyForFiltered(m.messages, m.messages, 0): true,
	}

	done := make(chan struct{})
	go func() {
		// Force the displayTruncate clamp path with a width that makes
		// width-10 < 4 (so the helper clamps to 4 and a wide-rune first char
		// returns "..." with empty visible prefix).
		_ = m.viewMessages(12)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("viewMessages did not return within 2s (likely infinite-loop in chunk wrap)")
	}
}

// TestExpandedMessagePreservesContentAtBoundary regresses the bug where
// `len(chunk) == len(remaining)` falsely concluded "fits" when
// displayTruncate cut at i == len(s)-3 (chunk and original same length but
// content differs). Before the fix, the trailing N bytes were dropped from
// the rendered output.
func TestExpandedMessagePreservesContentAtBoundary(t *testing.T) {
	db := newTestDB(t)

	m := NewModel(db, nil)
	m.sessions = []storage.SessionRow{{SessionID: "edge", Platform: "claude", StartTime: time.Now()}}
	m.selectedSession = 0
	// width-10 = maxCols; pick width so maxCols equals content length, the
	// exact boundary that previously triggered the length-equality bug.
	const content = "abcdefgh" // len 8
	m.messages = []collector.UserMessage{{Timestamp: time.Now(), Content: content}}
	m.refreshFilteredViews()
	m.expandedCalls = map[string]bool{
		messageExpansionKeyForFiltered(m.messages, m.messages, 0): true,
	}

	out := m.viewMessages(18) // maxCols = 8

	// All 8 characters must appear in the output (possibly across two lines).
	// Before the fix, output was just "abcde..." and "fgh" was lost.
	for _, r := range content {
		if !strings.ContainsRune(out, r) {
			t.Errorf("rendered output missing %q from content %q: %s", r, content, out)
		}
	}
}

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
