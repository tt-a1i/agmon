package testutil_test

import (
	"testing"

	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// leakSink holds references so GC cannot reclaim the allocated slices.
// package-level prevents the closure from being inlined away.
var leakSink [][]byte

func TestMemLeakCheckPassesIfStable(t *testing.T) {
	// Workload that allocates transiently — GC reclaims everything.
	testutil.MemLeakCheck(t, func() {
		s := make([]byte, 4096)
		_ = s // escapes to heap but has no surviving reference
	})
}

func TestMemLeakCheckDetectsLeak(t *testing.T) {
	leakSink = nil
	defer func() { leakSink = nil }()

	inner := &testing.T{}
	// Accumulate 10 KB per round into a package-level slice so GC cannot
	// reclaim it. With 200 rounds that's 2 MB retained.
	testutil.MemLeakCheck(inner, func() {
		leakSink = append(leakSink, make([]byte, 10*1024))
	}, testutil.MemLeakOpts{
		Rounds:     200,
		PercentMax: 10.0,
		BytesMax:   512 * 1024, // 512 KB floor — well below the 2 MB we'll retain
	})

	if !inner.Failed() {
		t.Error("MemLeakCheck should have reported a heap leak but did not")
	}
}

func TestMemLeakCheckRespectsThreshold(t *testing.T) {
	// Workload retains a small amount but well within a generous threshold.
	var tiny []byte
	inner := &testing.T{}
	testutil.MemLeakCheck(inner, func() {
		tiny = append(tiny, 1) // ~100 bytes total after 100 rounds
	}, testutil.MemLeakOpts{
		Rounds:     100,
		PercentMax: 100.0,   // allow 100% growth
		BytesMax:   5 << 20, // 5 MiB absolute floor
	})
	_ = tiny

	if inner.Failed() {
		t.Error("MemLeakCheck should not report a leak when heap growth is within threshold")
	}
}
