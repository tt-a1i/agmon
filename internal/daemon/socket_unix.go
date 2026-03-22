//go:build !windows

package daemon

import (
	"net"
	"os"
	"path/filepath"
)

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agmon", "agmon.sock")
}

func listenSocket(path string) (net.Listener, error) {
	os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
