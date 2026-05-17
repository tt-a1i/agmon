package daemon

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWalCheckpointLoopStopsOnDone(t *testing.T) {
	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))
	close(d.done)

	done := make(chan struct{})
	go func() {
		d.walCheckpointLoop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("walCheckpointLoop did not stop after done closed")
	}
}
