package collector

import (
	"strings"
	"testing"
	"time"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
	}{
		{"ascii mid cut", "abcdefgh", 4},
		{"chinese mid cut", "中文测试abc", 4},
		{"emoji mid cut", "🎉🎉🎉", 5},
		{"single rune larger than maxLen", "🎉abc", 2},
		{"mixed multibyte and ascii", "hello 世界 🎉 ok", 10},
		{"maxLen at exact rune boundary", "中a", 3},
		{"long multibyte cut", strings.Repeat("中", 1000), 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := truncate(tt.input, tt.maxLen)
			if len(tt.input) <= tt.maxLen {
				if out != tt.input {
					t.Errorf("short input mutated: got %q want %q", out, tt.input)
				}
				return
			}
			if len(out) < 3 || out[len(out)-3:] != "..." {
				t.Fatalf("missing trailing ellipsis: %q", out)
			}
			prefix := out[:len(out)-3]
			if len(prefix) > 0 && len(prefix) < len(tt.input) {
				if !isRuneStartByte(tt.input[len(prefix)]) {
					t.Errorf("cut splits a rune at byte %d (0x%02x is a continuation byte)", len(prefix), tt.input[len(prefix)])
				}
			}
		})
	}
}

func isRuneStartByte(b byte) bool {
	// ASCII or UTF-8 lead byte (0xxxxxxx, 110xxxxx, 1110xxxx, 11110xxx).
	if b < 0x80 || b >= 0xC0 {
		return true
	}
	return false
}

func TestParseTimestampReturnsFalseOnInvalid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantOK bool
		wantTS string
	}{
		{"empty", "", false, ""},
		{"garbage", "not a date", false, ""},
		{"rfc3339nano valid", "2026-01-14T12:07:10.150Z", true, "2026-01-14T12:07:10.15Z"},
		{"rfc3339 valid", "2026-01-14T12:07:10Z", true, "2026-01-14T12:07:10Z"},
		{"date only invalid", "2026-01-14", false, ""},
		{"trailing junk invalid", "2026-01-14T12:07:10Z extra", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, ok := parseTimestamp(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (input=%q)", ok, tt.wantOK, tt.input)
			}
			if !ok && !ts.IsZero() {
				t.Errorf("on failure ts must be zero, got %v", ts)
			}
			if tt.wantTS != "" {
				got := ts.UTC().Format(time.RFC3339Nano)
				if got != tt.wantTS {
					t.Errorf("ts = %q, want %q", got, tt.wantTS)
				}
			}
		})
	}
}

func TestExtractPatchFileChangesHandlesOversizedLines(t *testing.T) {
	// Build a patch whose body contains lines exceeding bufio.Scanner's
	// default 64KB cap, followed by another file header that must still be
	// recognized after the oversized lines.
	big := make([]byte, 200*1024)
	for i := range big {
		big[i] = 'x'
	}
	patch := "*** Update File: a.txt\n" +
		"@@\n" +
		"-" + string(big) + "\n" +
		"+" + string(big) + "\n" +
		"*** Update File: b.txt\n" +
		"@@\n" +
		"-bye\n" +
		"+hi\n"

	changes := extractPatchFileChanges(patch)
	gotPaths := make(map[string]bool)
	for _, c := range changes {
		gotPaths[c.Path] = true
	}
	if !gotPaths["a.txt"] {
		t.Errorf("missing a.txt in changes: %v", changes)
	}
	if !gotPaths["b.txt"] {
		t.Errorf("oversized line ate the b.txt header (regression): %v", changes)
	}
}
