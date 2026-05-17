package testutil_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func TestEventually_succeeds(t *testing.T) {
	var counter int32
	testutil.Eventually(t, func() bool {
		return atomic.AddInt32(&counter, 1) >= 3
	}, time.Second, 10*time.Millisecond, "counter should reach 3")
}

func TestEventually_immediateSuccess(t *testing.T) {
	called := false
	testutil.Eventually(t, func() bool {
		called = true
		return true
	}, time.Second, 10*time.Millisecond)
	if !called {
		t.Fatal("fn was never called")
	}
}

func TestEventually_retriesBeforeSuccess(t *testing.T) {
	var count int32
	testutil.Eventually(t, func() bool {
		n := atomic.AddInt32(&count, 1)
		return n >= 5
	}, time.Second, 5*time.Millisecond)
	if count < 5 {
		t.Fatalf("expected at least 5 calls, got %d", count)
	}
}

func TestEventuallyNoError_succeeds(t *testing.T) {
	var count int32
	testutil.EventuallyNoError(t, func() error {
		if atomic.AddInt32(&count, 1) < 3 {
			return errors.New("not ready")
		}
		return nil
	}, time.Second, 10*time.Millisecond)
}

func TestEventuallyNoError_immediateSuccess(t *testing.T) {
	testutil.EventuallyNoError(t, func() error { return nil }, time.Second, 10*time.Millisecond)
}

func TestEventually_noMessageVariant(t *testing.T) {
	var ready int32
	go func() {
		time.Sleep(20 * time.Millisecond)
		atomic.StoreInt32(&ready, 1)
	}()
	testutil.Eventually(t, func() bool {
		return atomic.LoadInt32(&ready) == 1
	}, time.Second, 5*time.Millisecond)
}
