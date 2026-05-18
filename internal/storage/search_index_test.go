package storage

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestFTS5SearchPrefixMatch(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("fts-prefix", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-prefix-call", "agent", "fts-prefix", "Edit", "Edit file in place", now); err != nil {
		t.Fatalf("insert call: %v", err)
	}

	hits, err := db.SearchHits("Ed*", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits len: got %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].Kind != "tool_param" {
		t.Fatalf("kind: got %q, want tool_param", hits[0].Kind)
	}
}

func TestFTS5SearchPhraseMatch(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("fts-phrase", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-phrase-call", "agent", "fts-phrase", "Bash", "say hello world now", now); err != nil {
		t.Fatalf("insert call: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-phrase-miss", "agent", "fts-phrase", "Bash", "hello brave world", now.Add(time.Second)); err != nil {
		t.Fatalf("insert miss call: %v", err)
	}

	hits, err := db.SearchHits(`"hello world"`, 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits len: got %d, want 1: %#v", len(hits), hits)
	}
	if hits[0].SessionID != "fts-phrase" || hits[0].Kind != "tool_param" {
		t.Fatalf("unexpected hit: %#v", hits[0])
	}
}

func TestFTS5SearchEscapesUserInput(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("fts-escape", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-escape-call", "agent", "fts-escape", "Bash", `quoted " needle`, now); err != nil {
		t.Fatalf("insert call: %v", err)
	}

	if _, err := db.SearchHits(`quoted " needle`, 10); err != nil {
		t.Fatalf("search with user quote returned error: %v", err)
	}
}

func TestFTS5SearchSnippetHighlight(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("fts-snippet", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-snippet-call", "agent", "fts-snippet", "Bash", "before needle after", now); err != nil {
		t.Fatalf("insert call: %v", err)
	}

	hits, err := db.SearchHits("needle", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits len: got %d, want 1: %#v", len(hits), hits)
	}
	if !strings.Contains(hits[0].Excerpt, "<mark>needle</mark>") {
		t.Fatalf("excerpt %q does not contain highlighted needle", hits[0].Excerpt)
	}
}

func TestFTS5BackfillsOnUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modernc sqlite WAL release timing causes this upgrade test to hang on Windows runners (same root cause as TestAddColumnIfMissingViaPragma in collector/round4_test.go)")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	now := time.Now().UTC()
	if err := db.UpsertSession("fts-backfill", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-backfill-call", "agent", "fts-backfill", "Bash", "backfillneedle param", now); err != nil {
		t.Fatalf("insert call: %v", err)
	}
	if err := db.InsertFileChange("fts-backfill", "/tmp/backfillneedle.go", event.FileEdit, now.Add(time.Second)); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if _, err := db.db.Exec(`DELETE FROM search_index`); err != nil {
		t.Fatalf("clear search index: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()

	hits, err := db.SearchHits("backfillneedle", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("hits len: got %d, want at least 2: %#v", len(hits), hits)
	}
}

func TestFTS5DeindexOnCleanSession(t *testing.T) {
	db := testDB(t)
	old := time.Now().UTC().AddDate(0, 0, -30)
	if err := db.UpsertSession("fts-clean", event.PlatformClaude, old); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("fts-clean-call", "agent", "fts-clean", "Bash", "cleanneedle", old); err != nil {
		t.Fatalf("insert call: %v", err)
	}
	if err := db.EndSession("fts-clean", old.Add(time.Second)); err != nil {
		t.Fatalf("end session: %v", err)
	}

	deleted, err := db.CleanOldSessions(7)
	if err != nil {
		t.Fatalf("clean old sessions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted sessions: got %d, want 1", deleted)
	}
	hits, err := db.SearchHits("cleanneedle", 10)
	if err != nil {
		t.Fatalf("search hits: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits len after clean: got %d, want 0: %#v", len(hits), hits)
	}
}

func BenchmarkSearchHitsFTS5(b *testing.B) {
	db := seedSearchBench(b, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.SearchHits("searchneedle", 50); err != nil {
			b.Fatalf("search hits: %v", err)
		}
	}
}

func BenchmarkSearchHitsLike(b *testing.B) {
	db := seedSearchBench(b, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.searchHitsLike("searchneedle", 50); err != nil {
			b.Fatalf("search hits: %v", err)
		}
	}
}

func seedSearchBench(b *testing.B, rows int) *DB {
	b.Helper()
	db := benchmarkDB(b)
	now := time.Now().UTC()
	if err := db.UpsertSession("bench-search", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < rows; i++ {
		content := fmt.Sprintf("ordinary command body %05d", i)
		if i%100 == 0 {
			content = fmt.Sprintf("searchneedle command body %05d", i)
		}
		if _, err := db.InsertToolCallStart(fmt.Sprintf("bench-search-%05d", i), "agent", "bench-search", "Bash", content, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			b.Fatalf("insert call %d: %v", i, err)
		}
	}
	return db
}
