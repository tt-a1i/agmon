package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkParseClaudeFileEvents measures the hot path used by the
// ClaudeLogWatcher's parallel initial scan and the incremental processFile —
// pulled out via the public ParseClaudeFileEvents for offline tooling.
// Future regressions in JSON parsing or shared helper logic show up here.
func BenchmarkParseClaudeFileEvents(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// 500-line synthetic session — mix of assistant + non-assistant.
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		if i%5 == 0 {
			// non-assistant line (skipped by parser)
			fmt.Fprintf(&sb,
				`{"type":"user","sessionId":"s","uuid":"u%d","gitBranch":"main","timestamp":"2026-01-14T12:07:%02dZ"}`+"\n",
				i, i%60)
		} else {
			fmt.Fprintf(&sb,
				`{"type":"assistant","sessionId":"s","uuid":"u%d","gitBranch":"main","timestamp":"2026-01-14T12:07:%02dZ","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d}}}`+"\n",
				i, i%60, 100+i, 50+i, i*10)
		}
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatalf("write: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events := ParseClaudeFileEvents(path, "s")
		if len(events) == 0 {
			b.Fatalf("expected events, got 0")
		}
	}
}

// BenchmarkExtractPatchFileChanges measures the apply_patch parser used per
// codex tool call. Performance matters because each codex apply_patch
// invocation runs this synchronously inside the watcher loop.
func BenchmarkExtractPatchFileChanges(b *testing.B) {
	// 50 file changes mixed across update/add/delete.
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		switch i % 3 {
		case 0:
			fmt.Fprintf(&sb, "*** Update File: path/to/file_%d.go\n", i)
		case 1:
			fmt.Fprintf(&sb, "*** Add File: path/to/new_%d.go\n", i)
		case 2:
			fmt.Fprintf(&sb, "*** Delete File: path/to/old_%d.go\n", i)
		}
		// realistic patch body lines
		for j := 0; j < 20; j++ {
			fmt.Fprintf(&sb, "-line %d before\n+line %d after\n", j, j)
		}
	}
	patch := sb.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		changes := extractPatchFileChanges(patch)
		if len(changes) == 0 {
			b.Fatalf("expected changes, got 0")
		}
	}
}

// BenchmarkTruncateRuneSafe measures the per-event truncate that runs on
// every tool call params/result. Regressions in rune-boundary scanning
// would slow the entire ingest path proportionally.
func BenchmarkTruncateRuneSafe(b *testing.B) {
	// Mix of ASCII + multi-byte to exercise the rune-boundary loop.
	input := strings.Repeat("hello 世界 🎉 ok ", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := truncate(input, 500)
		if len(out) == 0 {
			b.Fatal("truncate returned empty")
		}
	}
}
