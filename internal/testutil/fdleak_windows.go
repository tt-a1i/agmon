//go:build windows

package testutil

// FDSnapshot is a no-op on Windows (no /proc or /dev/fd equivalent).
func FDSnapshot() ([]string, error) {
	return nil, nil
}
