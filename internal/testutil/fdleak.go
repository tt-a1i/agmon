package testutil

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// FDLeakCheck snapshots file descriptors before a test and returns a cleanup
// func that verifies no new FDs remain open after the test completes.
//
// Usage: defer testutil.FDLeakCheck(t)()
//
// On Windows the check is always a no-op. On macOS/Linux it enumerates open
// file descriptors via fcntl(F_GETFD) without opening any new FDs itself.
func FDLeakCheck(t *testing.T) func() {
	t.Helper()
	before, err := FDSnapshot()
	if err != nil {
		t.Logf("FD snapshot unavailable: %v (skipping fd leak check)", err)
		return func() {}
	}
	return func() {
		t.Helper()
		after, err := FDSnapshot()
		if err != nil {
			return
		}
		leaked := fdDiff(before, after)
		if len(leaked) > 0 {
			t.Errorf("file descriptor leak: %d new FD(s):\n  %s",
				len(leaked), strings.Join(leaked, "\n  "))
		}
	}
}

// fdDiff returns items in current that are not accounted for by before
// (multiset semantics).
func fdDiff(before, current []string) []string {
	counts := make(map[string]int, len(before))
	for _, p := range before {
		counts[p]++
	}
	var leaked []string
	for _, p := range current {
		if counts[p] > 0 {
			counts[p]--
			continue
		}
		leaked = append(leaked, p)
	}
	sort.Strings(leaked)
	return leaked
}

// FormatFDs formats a list of FD identifiers for debugging.
func FormatFDs(fds []string) string {
	if len(fds) == 0 {
		return "<none>"
	}
	return fmt.Sprintf("%d fd(s):\n  %s", len(fds), strings.Join(fds, "\n  "))
}
