//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

func DefaultSocketPath() string {
	return appdir.PathFor("tokenmeter.sock", "agmon.sock")
}

type socketLock struct {
	file *os.File
}

func acquireSocketLock(path string) (*socketLock, error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, os.ErrExist
		}
		return nil, err
	}
	return &socketLock{file: f}, nil
}

func (l *socketLock) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		if conn, dialErr := net.Dial("unix", path); dialErr == nil {
			conn.Close()
			return nil, os.ErrExist
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, removeErr
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Restrict so only the owning user can connect. net.Listen creates the
	// socket file with 0666 & ~umask (typically 0644), which on Linux and
	// macOS (10.5+) lets group/world open the socket and write fake events
	// into the daemon. Chmod-after-Listen leaves a µs-level TOCTOU window
	// where a same-host attacker who's already racing connect() could slip
	// in — practically unreachable, but if you need strict guarantees wrap
	// the Listen in syscall.Umask(0o077). For local dev tooling 0600 is
	// sufficient.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		os.Remove(path)
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

func dialSocket(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

func cleanupSocket(path string) {
	os.Remove(path)
}
