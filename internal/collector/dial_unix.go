//go:build !windows

package collector

import (
	"net"
	"time"
)

func dialDaemon(sockPath string) (net.Conn, error) {
	return net.DialTimeout("unix", sockPath, 2*time.Second)
}
