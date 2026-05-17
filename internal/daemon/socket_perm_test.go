//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// TestSocketChmodToOwnerOnly verifies the listening socket is mode 0600 so
// other local users cannot connect and inject fake events.
func TestSocketChmodToOwnerOnly(t *testing.T) {
	// macOS limits unix socket paths to ~104 bytes; t.TempDir() under
	// /var/folders/... is already that long. Use a short /tmp path instead.
	dir := t.TempDir()
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("tokenmeter-permtest-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(subscriberSocketPath(sockPath))
	})

	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer d.Stop()

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("socket perm = %o, want 0600 (any other mode lets local users connect)", mode)
	}

	subInfo, err := os.Stat(subscriberSocketPath(sockPath))
	if err != nil {
		t.Fatalf("stat sub socket: %v", err)
	}
	if subMode := subInfo.Mode().Perm(); subMode != 0o600 {
		t.Errorf("subscriber socket perm = %o, want 0600", subMode)
	}
}
