package testutil_test

import (
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func TestLeakCheckCleanRun(t *testing.T) {
	cleanup := testutil.LeakCheck(t)
	// no new goroutines — cleanup should not report any leaks
	cleanup()
}

func TestLeakCheckDetectsLeak(t *testing.T) {
	blocked := make(chan struct{})
	started := make(chan struct{})

	inner := &testing.T{}
	cleanup := testutil.LeakCheck(inner)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		<-blocked // never receives; intentional leak for this test
	}()
	<-started

	// Run cleanup: should detect the leaked goroutine.
	// (The goroutine above will be collected after the test when blocked is GC'd,
	//  but during cleanup() it is still alive.)
	cleanup()

	// Signal cleanup and wait so we don't leave a goroutine after the test.
	close(blocked)
	wg.Wait()

	if !inner.Failed() {
		t.Error("expected LeakCheck to report a leak, but it did not")
	}
}

func TestLeakCheckWaitsForGoroutinesToExit(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})

	inner := &testing.T{}
	cleanup := testutil.LeakCheck(inner)

	go func() {
		close(started)
		time.Sleep(200 * time.Millisecond) // exits well within the 2s grace window
		close(done)
	}()
	<-started

	cleanup()

	<-done
	if inner.Failed() {
		t.Error("LeakCheck incorrectly reported a leak for a goroutine that exited in time")
	}
}

func TestLeakCheckIgnoresFrameworkStacks(t *testing.T) {
	// The test runner itself has goroutines; a clean LeakCheck should not flag them.
	inner := &testing.T{}
	cleanup := testutil.LeakCheck(inner)
	// Simulate some work inside a test (no extra goroutines).
	time.Sleep(10 * time.Millisecond)
	cleanup()
	if inner.Failed() {
		t.Error("LeakCheck falsely flagged testing-framework goroutines as leaks")
	}
}
