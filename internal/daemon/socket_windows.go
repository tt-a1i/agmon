//go:build windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

// On Windows, use TCP localhost instead of Unix socket.
// The "socket path" is repurposed to store the port file location.

// Use OS-assigned ports (":0") to avoid bind collisions when multiple
// daemons run on the same Windows host (tests, parallel installs).
// The actual port is written to the port file by listenSocket and
// resolved by dialSocket, so callers never depend on a fixed number.
const listenAddr = "127.0.0.1:0"
const subscriberListenAddr = "127.0.0.1:0"

func DefaultSocketPath() string {
	return appdir.PathFor("tokenmeter.port", "agmon.port")
}

// socketLock holds an exclusively-created lock file. Closing the lock
// deletes the file so another daemon can claim the same socket path.
type socketLock struct {
	file *os.File
	path string
}

// acquireSocketLock uses O_CREATE|O_EXCL on a sidecar ".lock" file as
// a Windows-friendly mutex. If the lock file already exists, we check
// the PID it holds; if that process is still running we return
// os.ErrExist (matching the Unix flock semantics in socket_unix.go).
// Stale lock files (PID dead) are removed and recreated.
func acquireSocketLock(path string) (*socketLock, error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
		if pidIsAlive(readLockPID(lockPath)) {
			return nil, os.ErrExist
		}
		// Stale lock — owner is gone.
		_ = os.Remove(lockPath)
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return nil, os.ErrExist
			}
			return nil, err
		}
	}
	if _, werr := fmt.Fprintf(f, "%d", os.Getpid()); werr != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, werr
	}
	return &socketLock{file: f, path: lockPath}, nil
}

func (l *socketLock) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
	_ = os.Remove(l.path)
	l.file = nil
}

func readLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func pidIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", tcpListenAddr(path))
	if err != nil {
		return nil, err
	}
	// Write the actual listen address to the port file so clients can find it.
	os.WriteFile(path, []byte(ln.Addr().String()), 0o644)
	return ln, nil
}

func dialSocket(path string) (net.Conn, error) {
	addr, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return net.Dial("tcp", string(addr))
}

func cleanupSocket(path string) {
	os.Remove(path)
}

func tcpListenAddr(path string) string {
	if strings.Contains(filepath.Base(path), ".events.") {
		return subscriberListenAddr
	}
	return listenAddr
}
