package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

var (
	daemonLogMaxBytes   int64 = 10 * 1024 * 1024
	daemonLogMaxBackups       = 5
)

// SetupLogFile tees daemon package logs to stderr and ~/.tokenmeter/daemon.log.
// The file writer rotates by size and keeps the newest five backups.
func SetupLogFile() (func() error, error) {
	path := appdir.Path("daemon.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	rotator := &rotatingLogWriter{
		path:       path,
		maxBytes:   daemonLogMaxBytes,
		maxBackups: daemonLogMaxBackups,
	}
	if err := rotator.open(); err != nil {
		return nil, err
	}

	prevWriter := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	log.SetOutput(io.MultiWriter(os.Stderr, rotator))

	return func() error {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		return rotator.Close()
	}, nil
}

type rotatingLogWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	if err := w.rotateIfNeeded(); err != nil {
		return n, err
	}
	return n, nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLogWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *rotatingLogWriter) rotateIfNeeded() error {
	if w.maxBytes <= 0 || w.maxBackups <= 0 {
		return nil
	}
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= w.maxBytes {
		return nil
	}

	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil

	for i := w.maxBackups; i >= 1; i-- {
		src := backupPath(w.path, i)
		dst := backupPath(w.path, i+1)
		if i == w.maxBackups {
			_ = os.Remove(src)
			continue
		}
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := os.Stat(w.path); err == nil {
		if err := os.Rename(w.path, backupPath(w.path, 1)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func backupPath(path string, n int) string {
	return path + "." + strconv.Itoa(n)
}
