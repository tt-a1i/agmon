package testutil

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// MemLeakOpts configures MemLeakCheck thresholds and iteration count.
type MemLeakOpts struct {
	// Rounds is the number of workload invocations after the warmup run.
	// Defaults to 100.
	Rounds int

	// PercentMax is the maximum allowed HeapAlloc growth as a percentage of
	// the baseline. Defaults to 10.0 (10%).
	PercentMax float64

	// BytesMax is the maximum allowed absolute HeapAlloc growth in bytes.
	// Defaults to 1 MiB. This floor prevents false positives when the baseline
	// is very small and percentage noise dominates.
	BytesMax uint64
}

// MemLeakCheck runs workload Rounds times and verifies that heap allocation
// does not grow beyond threshold after GC.
//
// A warmup invocation is performed before the baseline snapshot to allow
// lazy initialisation (driver caches, compilation, etc.) to settle so it
// does not inflate the measured delta.
//
// The growth limit is max(PercentMax% of baseline, BytesMax). This avoids
// false positives on tiny baselines while still catching large absolute leaks.
//
// Usage:
//
//	testutil.MemLeakCheck(t, func() {
//	    _, _ = db.ListSessions()
//	})
func MemLeakCheck(t *testing.T, workload func(), opts ...MemLeakOpts) {
	t.Helper()
	o := MemLeakOpts{Rounds: 100, PercentMax: 10.0, BytesMax: 1 << 20}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Rounds <= 0 {
		o.Rounds = 100
	}
	if o.PercentMax <= 0 {
		o.PercentMax = 10.0
	}
	if o.BytesMax == 0 {
		o.BytesMax = 1 << 20
	}

	// Warmup: one invocation to let lazy initialisations (SQLite driver,
	// HTTP mux, etc.) complete before we snapshot the baseline.
	workload()
	forceGC()

	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	for range o.Rounds {
		workload()
	}
	forceGC()

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if after.HeapAlloc <= base.HeapAlloc {
		return // heap shrank or stayed the same — always OK
	}
	grown := after.HeapAlloc - base.HeapAlloc

	// Threshold = max(absolute floor, percentage of baseline).
	absLimit := o.BytesMax
	pctLimit := uint64(float64(base.HeapAlloc) * o.PercentMax / 100.0)
	limit := absLimit
	if pctLimit > limit {
		limit = pctLimit
	}

	if grown > limit {
		t.Errorf("heap memory leak: %d rounds, base=%s, after=%s, grown=%d KB (limit=%d KB / %.1f%%)",
			o.Rounds,
			FormatMemStats(base),
			FormatMemStats(after),
			grown/1024,
			limit/1024,
			float64(grown)/float64(max(base.HeapAlloc, uint64(1)))*100,
		)
	}
}

// forceGC runs GC three times with short sleeps to let finalizers drain.
func forceGC() {
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

// FormatMemStats returns a compact summary of key MemStats fields.
func FormatMemStats(m runtime.MemStats) string {
	return fmt.Sprintf("HeapAlloc=%dKB HeapInuse=%dKB NumGC=%d",
		m.HeapAlloc/1024, m.HeapInuse/1024, m.NumGC)
}
