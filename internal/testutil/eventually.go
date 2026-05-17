package testutil

import (
	"fmt"
	"testing"
	"time"
)

// Eventually polls fn every interval until it returns true or timeout elapses.
// Replaces time.Sleep-based waits for more reliable test synchronization.
func Eventually(t *testing.T, fn func() bool, timeout, interval time.Duration, msg ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	if len(msg) > 0 {
		t.Fatalf("Eventually timeout after %s: %s", timeout, fmt.Sprint(msg...))
	} else {
		t.Fatalf("Eventually timeout after %s", timeout)
	}
}

// EventuallyNoError polls fn every interval until fn returns nil or timeout elapses.
// Useful when waiting for an operation that may transiently fail.
func EventuallyNoError(t *testing.T, fn func() error, timeout, interval time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = fn(); lastErr == nil {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("EventuallyNoError timeout after %s: %v", timeout, lastErr)
}
