package appdir

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPathJoinsElems verifies Path concatenates Base() with the supplied
// elements via filepath.Join.
func TestPathJoinsElems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	// Single element
	got := Path("data.db")
	want := filepath.Join(home, ".tokenmeter", "data.db")
	if got != want {
		t.Errorf("Path(\"data.db\") = %q, want %q", got, want)
	}

	// Multi-element
	got = Path("data", "sessions", "x.jsonl")
	if !strings.HasSuffix(got, filepath.Join(".tokenmeter", "data", "sessions", "x.jsonl")) {
		t.Errorf("multi-element path missing nested elements: %q", got)
	}

	// No elements
	got = Path()
	if got != filepath.Join(home, ".tokenmeter") {
		t.Errorf("no-arg Path = %q, want %q", got, filepath.Join(home, ".tokenmeter"))
	}
}
