package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tt-a1i/agmon/internal/event"
)

func TestReadUserMessagesClaudeFallsBackToMatchingSessionFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessionID := "788dbad1-b6ae-4541-bee8-c621ab6a1c13"
	rootCWD := "/Users/admin/code/coding-cli-guide"
	wrongCWD := rootCWD + "/src"
	logPath := filepath.Join(
		home,
		".claude",
		"projects",
		strings.ReplaceAll(rootCWD, "/", "-"),
		sessionID+".jsonl",
	)

	writeLines(t, logPath,
		`{"type":"user","isMeta":false,"timestamp":"2026-03-22T10:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"Done","is_error":false}]}}`,
		`{"type":"user","isMeta":false,"timestamp":"2026-03-22T10:01:00Z","message":{"content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}}`,
	)

	got := ReadUserMessages(event.PlatformClaude, sessionID, wrongCWD, 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Content != "第一段\n\n第二段" {
		t.Fatalf("unexpected content: %q", got[0].Content)
	}
}

func TestReadUserMessagesCodexExtractsUserInputText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessionID := "d4430cef-110d-42e0-924a-bfceeba0c4e1"
	logPath := filepath.Join(
		home,
		".codex",
		"sessions",
		"2026",
		"03",
		"22",
		"rollout-2026-03-22T09-00-00-"+sessionID+".jsonl",
	)

	writeLines(t, logPath,
		`{"timestamp":"2026-03-22T09:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/project"}]}}`,
		`{"timestamp":"2026-03-22T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp/project</cwd>\n</environment_context>"}]}}`,
		`{"timestamp":"2026-03-22T09:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"修这个 bug"},{"type":"input_text","text":"顺便补测试"}]}}`,
	)

	got := ReadUserMessages(event.PlatformCodex, sessionID, "", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Content != "修这个 bug\n\n顺便补测试" {
		t.Fatalf("unexpected content: %q", got[0].Content)
	}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
