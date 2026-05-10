//go:build !windows

package daemon

import (
	"errors"
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
	return net.Listen("unix", path)
}

func dialSocket(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

func cleanupSocket(path string) {
	os.Remove(path)
}
