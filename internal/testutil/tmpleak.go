package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TmpFileLeakCheck verifies that no new files matching /tmp/<prefix>* are
// created during a test without being cleaned up.
//
// Usage: defer testutil.TmpFileLeakCheck(t, "myprefix-")()
func TmpFileLeakCheck(t *testing.T, prefix string) func() {
	t.Helper()
	before := listTmp(prefix)
	return func() {
		t.Helper()
		after := listTmp(prefix)
		leaked := strSetDiff(after, before)
		if len(leaked) > 0 {
			t.Errorf("tmp file leak: %d new file(s) matching %s* in %s:\n  %s",
				len(leaked), prefix, os.TempDir(), strings.Join(leaked, "\n  "))
		}
	}
}

func listTmp(prefix string) []string {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), prefix+"*"))
	return matches
}

// strSetDiff returns elements in a that are not in b (set semantics).
func strSetDiff(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, x := range b {
		bset[x] = struct{}{}
	}
	var d []string
	for _, x := range a {
		if _, ok := bset[x]; !ok {
			d = append(d, x)
		}
	}
	return d
}
