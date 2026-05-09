package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}

func TestBaseUsesCurrentDirForNewInstalls(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	want := filepath.Join(home, CurrentDir)
	if got := Base(); got != want {
		t.Fatalf("Base() = %q, want %q", got, want)
	}
}

func TestBaseFallsBackToLegacyDir(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	legacy := filepath.Join(home, LegacyDir)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := Base(); got != legacy {
		t.Fatalf("Base() = %q, want %q", got, legacy)
	}
	if !UsingLegacy() {
		t.Fatal("UsingLegacy() = false, want true")
	}
	gotPath := PathFor("tokenmeter.db", "agmon.db", "data")
	wantPath := filepath.Join(legacy, "data", "agmon.db")
	if gotPath != wantPath {
		t.Fatalf("PathFor() = %q, want %q", gotPath, wantPath)
	}
}

func TestBasePrefersCurrentDirOverLegacyDir(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	current := filepath.Join(home, CurrentDir)
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, LegacyDir), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := Base(); got != current {
		t.Fatalf("Base() = %q, want %q", got, current)
	}
}
