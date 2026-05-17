package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestSearchHitsAcrossKinds(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("search-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpdateSessionMeta("search-session", "/Users/test/project-search", "feature/search"); err != nil {
		t.Fatalf("update meta: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-param", "agent", "search-session", "Bash", "run needle command", now); err != nil {
		t.Fatalf("insert param call: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-result", "agent", "search-session", "Read", "no match here", now.Add(time.Second)); err != nil {
		t.Fatalf("insert result call: %v", err)
	}
	if err := db.UpdateToolCallEnd("call-result", "needle result output", event.StatusSuccess, 10, now.Add(2*time.Second)); err != nil {
		t.Fatalf("end result call: %v", err)
	}
	if err := db.InsertFileChange("search-session", "/tmp/needle-file.go", event.FileEdit, now.Add(3*time.Second)); err != nil {
		t.Fatalf("insert file change: %v", err)
	}

	hits, err := db.SearchHits("needle", 50)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}

	kinds := map[string]bool{}
	for _, hit := range hits {
		kinds[hit.Kind] = true
		if hit.SessionID != "search-session" {
			t.Fatalf("hit session id: got %q, want search-session", hit.SessionID)
		}
		if hit.SessionName != "feature/search" {
			t.Fatalf("session name: got %q, want feature/search", hit.SessionName)
		}
		if hit.Platform != string(event.PlatformClaude) {
			t.Fatalf("platform: got %q, want claude", hit.Platform)
		}
		if !strings.Contains(strings.ToLower(hit.Excerpt), "needle") {
			t.Fatalf("excerpt %q does not include query", hit.Excerpt)
		}
	}
	for _, kind := range []string{"tool_param", "tool_result", "file"} {
		if !kinds[kind] {
			t.Fatalf("missing kind %q in hits: %#v", kind, hits)
		}
	}
}

func TestSearchHitsExcerptTrimsLongContent(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("long-session", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	longText := strings.Repeat("a", 120) + "\nneedle\n" + strings.Repeat("b", 120)
	if _, err := db.InsertToolCallStart("long-call", "agent", "long-session", "Bash", longText, now); err != nil {
		t.Fatalf("insert call: %v", err)
	}

	hits, err := db.SearchHits("needle", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits len: got %d, want 1: %#v", len(hits), hits)
	}
	if len(hits[0].Excerpt) > 80 {
		t.Fatalf("excerpt length: got %d, want <= 80: %q", len(hits[0].Excerpt), hits[0].Excerpt)
	}
	if strings.Contains(hits[0].Excerpt, "\n") {
		t.Fatalf("excerpt contains newline: %q", hits[0].Excerpt)
	}
}

func TestSearchHitsEscapesLikeWildcards(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("literal-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert literal session: %v", err)
	}
	if err := db.UpsertSession("wildcard-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert wildcard session: %v", err)
	}
	if _, err := db.InsertToolCallStart("literal-call", "agent", "literal-session", "Bash", "contains abc% literal", now); err != nil {
		t.Fatalf("insert literal call: %v", err)
	}
	if _, err := db.InsertToolCallStart("wildcard-call", "agent", "wildcard-session", "Bash", "contains abcXYZ wildcard candidate", now); err != nil {
		t.Fatalf("insert wildcard call: %v", err)
	}

	hits, err := db.SearchHits("abc%", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits len: got %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].SessionID != "literal-session" {
		t.Fatalf("hit session: got %q, want literal-session", hits[0].SessionID)
	}
}

func TestSearchHitsLimitCap(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("cap-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 250; i++ {
		callID := "cap-call-" + time.Unix(int64(i), 0).UTC().Format("150405.000000000")
		if _, err := db.InsertToolCallStart(callID, "agent", "cap-session", "Bash", "needle", now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("insert call %d: %v", i, err)
		}
	}

	hits, err := db.SearchHits("needle", 500)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) > 200 {
		t.Fatalf("hits len: got %d, want <= 200", len(hits))
	}
	if len(hits) != 200 {
		t.Fatalf("hits len: got %d, want cap 200", len(hits))
	}
}
