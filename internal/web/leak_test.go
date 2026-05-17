package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func openLeakDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tm-web-lk-")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("open db: %v", err)
	}
	return db, func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}
}

func TestServerStartShutdownNoLeak(t *testing.T) {
	db, dbCleanup := openLeakDB(t)
	defer dbCleanup()
	defer testutil.LeakCheck(t)()

	srv := NewServer(db, "0")

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Wait briefly for the server goroutines to spin up.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not exit after Shutdown")
	}
}

func TestServerShutdownBeforeStartNoLeak(t *testing.T) {
	db, dbCleanup := openLeakDB(t)
	defer dbCleanup()
	defer testutil.LeakCheck(t)()

	srv := NewServer(db, "0")

	// Shutdown on a never-started server should be safe (returns immediately).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("shutdown of non-started server: %v", err)
	}
}
