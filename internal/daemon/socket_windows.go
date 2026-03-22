//go:build windows

package daemon

import (
	"net"
	"os"
	"path/filepath"
)

// On Windows, use TCP localhost instead of Unix socket.
// The "socket path" is repurposed to store the port file location.

const listenAddr = "127.0.0.1:19847"

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agmon", "agmon.port")
}

func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", listenAddr)
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
