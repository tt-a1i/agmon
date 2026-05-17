package testutil

import (
	"bytes"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// LeakCheck snapshots goroutines before a test and returns a cleanup func
// that verifies no new goroutines remain after the test completes.
//
// Usage: defer testutil.LeakCheck(t)()
// The outer call runs immediately (taking the before-snapshot);
// the inner defer fires at test end to compare.
func LeakCheck(t *testing.T) func() {
	t.Helper()
	runtime.GC()
	before := goroutineCounts(snapshotRaw())

	return func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			runtime.GC()
			after := goroutineCounts(snapshotRaw())
			leaks := diffCounts(before, after)
			leaks = filterFramework(leaks)
			if len(leaks) == 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}

		runtime.GC()
		after := goroutineCounts(snapshotRaw())
		leaks := filterFramework(diffCounts(before, after))
		if len(leaks) > 0 {
			total := 0
			for _, n := range leaks {
				total += n
			}
			t.Errorf("goroutine leak: %d leaked goroutines\n%s", total, formatLeaks(leaks))
		}
	}
}

// snapshotRaw returns a slice of raw goroutine stack strings (one per goroutine).
func snapshotRaw() []string {
	buf := make([]byte, 64*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, len(buf)*2)
	}

	var stacks []string
	var cur []byte
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("goroutine ")) {
			if len(cur) > 0 {
				stacks = append(stacks, string(cur))
			}
			cur = append(cur[:0], line...)
		} else if len(cur) > 0 {
			cur = append(cur, '\n')
			cur = append(cur, line...)
		}
	}
	if len(cur) > 0 {
		stacks = append(stacks, string(cur))
	}
	return stacks
}

// normalizeStack strips the goroutine ID and state so identical code paths
// compare equal regardless of goroutine number or wait reason.
func normalizeStack(s string) string {
	nl := strings.Index(s, "\n")
	if nl < 0 {
		return s
	}
	return s[nl+1:]
}

// goroutineCounts builds a multiset: normalized_stack → count.
func goroutineCounts(stacks []string) map[string]int {
	m := make(map[string]int, len(stacks))
	for _, s := range stacks {
		m[normalizeStack(s)]++
	}
	return m
}

// diffCounts returns new normalized stacks whose count increased relative
// to before, mapped to the number of new instances.
func diffCounts(before, after map[string]int) map[string]int {
	leaked := make(map[string]int)
	for k, n := range after {
		if extra := n - before[k]; extra > 0 {
			leaked[k] = extra
		}
	}
	return leaked
}

// filterFramework removes goroutines that belong to the Go test runner or
// standard-library background goroutines that are always present.
func filterFramework(leaks map[string]int) map[string]int {
	skipPrefixes := []string{
		"testing.tRunner",
		"testing.(*M).Run",
		"testing.(*B).launch",
		"testing.runFuzzTests",
		"runtime.goexit",
		"runtime/trace",
		"created by net/http.(*conn).serve",
		"created by net.(*netFD).connect",
		"created by os/signal.",
		"created by database/sql.(*DB).connectionOpener",
		"signal.loop",
		"runtime.ensureSigM",
		"runtime.runfinq",
		"runtime.forcegchelper",
		"runtime.bgsweep",
		"runtime.bgscavenge",
		"runtime.gcBgMarkWorker",
	}
	out := make(map[string]int, len(leaks))
	for k, n := range leaks {
		skip := false
		for _, p := range skipPrefixes {
			if strings.Contains(k, p) {
				skip = true
				break
			}
		}
		if !skip {
			out[k] = n
		}
	}
	return out
}

// formatLeaks formats the leaked goroutine stacks for display in test output.
func formatLeaks(leaks map[string]int) string {
	keys := make([]string, 0, len(leaks))
	for k := range leaks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	i := 1
	for _, k := range keys {
		fmt.Fprintf(&b, "--- leak %d (×%d) ---\n%s\n", i, leaks[k], k)
		i++
	}
	return b.String()
}
