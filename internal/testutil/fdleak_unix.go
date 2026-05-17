//go:build !windows

package testutil

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
)

// maxFDCheck is the upper bound for FD enumeration. Most test processes have
// far fewer than 256 open file descriptors.
const maxFDCheck = 256

// FDSnapshot returns the current set of open file descriptors for this process
// as a sorted slice of string identifiers.
//
// FDs are enumerated via fcntl(F_GETFD), which never opens any new file
// descriptors itself. On Linux, /proc/self/fd readlink is attempted after
// enumeration to provide richer names; common noisy types (sockets, pipes,
// anonymous inodes) are filtered out. On macOS the FD number is used as the
// identifier ("fd:N").
func FDSnapshot() ([]string, error) {
	// Enumerate via fcntl — works on both macOS and Linux without opening new FDs.
	var openNums []int
	for i := 0; i < maxFDCheck; i++ {
		_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(i), syscall.F_GETFD, 0)
		if errno == 0 {
			openNums = append(openNums, i)
		}
	}

	// On Linux, try to resolve to real paths via /proc/self/fd for richer output.
	if _, err := os.Stat("/proc/self/fd"); err == nil {
		return resolveLinuxFDs(openNums), nil
	}

	// macOS / other Unix: use "fd:N" identifiers.
	fds := make([]string, 0, len(openNums))
	for _, n := range openNums {
		fds = append(fds, fmt.Sprintf("fd:%d", n))
	}
	return fds, nil
}

// resolveLinuxFDs maps FD numbers to real paths via /proc/self/fd readlink,
// filtering out noisy FD types (sockets, pipes, anon inodes, tty devices).
func resolveLinuxFDs(nums []int) []string {
	var fds []string
	for _, n := range nums {
		link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", n))
		if err != nil {
			// FD was valid per fcntl but closed before readlink — skip.
			fds = append(fds, fmt.Sprintf("fd:%d", n))
			continue
		}
		if filterLinuxFD(link) {
			continue
		}
		fds = append(fds, link)
	}
	sort.Strings(fds)
	return fds
}

func filterLinuxFD(link string) bool {
	return strings.HasPrefix(link, "socket:") ||
		strings.HasPrefix(link, "anon_inode:") ||
		strings.HasPrefix(link, "pipe:") ||
		strings.HasPrefix(link, "/dev/null") ||
		strings.HasPrefix(link, "/dev/tty") ||
		strings.HasPrefix(link, "/dev/pts")
}
