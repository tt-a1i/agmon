//go:build windows

package collector

import (
	"net"
	"os"
	"time"
)

func dialDaemon(sockPath string) (net.Conn, error) {
	addr, err := os.ReadFile(sockPath)
	if err != nil {
		return nil, err
	}
	return net.DialTimeout("tcp", string(addr), 2*time.Second)
}
