package storage

import (
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

func TestFillSessionMetaKeepsProjectRootWhenNewCWDIsChild(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpdateSessionMeta("s1", "/Users/admin/code/project", "main"); err != nil {
		t.Fatalf("set root cwd: %v", err)
	}
	if err := db.FillSessionMeta("s1", "/Users/admin/code/project/src", ""); err != nil {
		t.Fatalf("set child cwd: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if sessions[0].CWD != "/Users/admin/code/project" {
		t.Fatalf("expected root cwd to be preserved, got %q", sessions[0].CWD)
	}
}

func TestUpdateSessionMetaAllowsAuthoritativeCorrection(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.FillSessionMeta("s1", "/Users/admin/code/project/src", "main"); err != nil {
		t.Fatalf("set child cwd: %v", err)
	}
	if err := db.UpdateSessionMeta("s1", "/Users/admin/code/project", ""); err != nil {
		t.Fatalf("set parent cwd: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if sessions[0].CWD != "/Users/admin/code/project" {
		t.Fatalf("expected parent cwd to replace child, got %q", sessions[0].CWD)
	}
}
