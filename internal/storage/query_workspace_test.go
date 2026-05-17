package storage

import (
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func seedWorkspaceSession(t *testing.T, db *DB, id, cwd string, start time.Time) {
	t.Helper()
	if err := db.UpsertSession(id, event.PlatformClaude, start); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
	if err := db.UpdateSessionMeta(id, cwd, "main"); err != nil {
		t.Fatalf("update meta %s: %v", id, err)
	}
	if err := db.InsertTokenUsage("agent-"+id, id, 10, 5, 0, 0, "sonnet", 0.01, start, "src-"+id); err != nil {
		t.Fatalf("insert tokens %s: %v", id, err)
	}
}

func TestListSessionsByWorkspaceMatchesExactCWD(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	seedWorkspaceSession(t, db, "exact", "/code/a", now)
	seedWorkspaceSession(t, db, "other", "/code/b", now.Add(time.Minute))

	got, err := db.ListSessionsByWorkspace("/code/a", 10)
	if err != nil {
		t.Fatalf("ListSessionsByWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "exact" {
		t.Fatalf("workspace exact match got %#v, want exact only", got)
	}
}

func TestListSessionsByWorkspaceMatchesSubdirs(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	seedWorkspaceSession(t, db, "root", "/code/a", now)
	seedWorkspaceSession(t, db, "child", "/code/a/service", now.Add(time.Minute))
	seedWorkspaceSession(t, db, "other", "/code/c", now.Add(2*time.Minute))

	got, err := db.ListSessionsByWorkspace("/code/a", 10)
	if err != nil {
		t.Fatalf("ListSessionsByWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("workspace subdir match len=%d, want 2: %#v", len(got), got)
	}
	if got[0].SessionID != "child" || got[1].SessionID != "root" {
		t.Fatalf("workspace subdir order/matches got %#v, want child then root", got)
	}
}

func TestListSessionsByWorkspaceNotPrefix(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	seedWorkspaceSession(t, db, "wanted", "/code/a/app", now)
	seedWorkspaceSession(t, db, "prefix", "/code/ab", now.Add(time.Minute))

	got, err := db.ListSessionsByWorkspace("/code/a", 10)
	if err != nil {
		t.Fatalf("ListSessionsByWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "wanted" {
		t.Fatalf("workspace should not match prefix sibling, got %#v", got)
	}
}
