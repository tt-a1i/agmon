package appdir

import (
	"os"
	"path/filepath"
)

const (
	CurrentDir = ".tokenmeter"
	LegacyDir  = ".agmon"
)

// Base returns the app data directory. Existing agmon installs keep using the
// legacy directory until a new TokenMeter directory is created.
func Base() string {
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, CurrentDir)
	if exists(current) {
		return current
	}

	legacy := filepath.Join(home, LegacyDir)
	if exists(legacy) {
		return legacy
	}

	return current
}

func Path(elem ...string) string {
	parts := append([]string{Base()}, elem...)
	return filepath.Join(parts...)
}

func PathFor(currentName, legacyName string, dirs ...string) string {
	name := currentName
	if UsingLegacy() {
		name = legacyName
	}
	parts := append([]string{Base()}, dirs...)
	parts = append(parts, name)
	return filepath.Join(parts...)
}

func UsingLegacy() bool {
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, CurrentDir)
	if exists(current) {
		return false
	}
	return exists(filepath.Join(home, LegacyDir))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
