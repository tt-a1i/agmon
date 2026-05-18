//go:build windows

package daemon

import (
	"net"
	"os"
	"path/filepath"
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

type socketLock struct{}

func acquireSocketLock(path string) (*socketLock, error) {
	return &socketLock{}, nil
}

func (l *socketLock) Close() {}

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
