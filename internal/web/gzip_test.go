package web

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// largeJSONHandler writes a JSON array large enough to benefit from compression.
func largeJSONHandler(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	items := make([]item, 200)
	for i := range items {
		items[i] = item{ID: i, Name: "session-name-long-string", Value: "some-repeated-value-data"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// TestGzipMiddlewareCompressesResponse verifies that when the client sends
// Accept-Encoding: gzip the middleware sets Content-Encoding: gzip and the
// response body decompresses to the original payload.
func TestGzipMiddlewareCompressesResponse(t *testing.T) {
	handler := gzipMiddleware(largeJSONHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", resp.Header.Get("Content-Encoding"))
	}

	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read decompressed body: %v", err)
	}
	if !strings.Contains(string(body), "session-name-long-string") {
		t.Errorf("decompressed body missing expected content; got prefix: %.80s", body)
	}

	// Compressed body must be smaller than the raw response.
	rawRec := httptest.NewRecorder()
	largeJSONHandler(rawRec, req)
	rawLen := rawRec.Body.Len()
	gzLen := w.Body.Len()
	if gzLen >= rawLen {
		t.Errorf("compressed size (%d) >= raw size (%d); gzip not helping", gzLen, rawLen)
	}
}

// TestGzipMiddlewareSkipsWithoutHeader verifies that without Accept-Encoding:
// gzip the response is served as-is (no Content-Encoding header set).
func TestGzipMiddlewareSkipsWithoutHeader(t *testing.T) {
	handler := gzipMiddleware(largeJSONHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	// No Accept-Encoding header.
	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("expected no Content-Encoding, got %q", enc)
	}
	// Body must be readable raw JSON.
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "session-name-long-string") {
		t.Errorf("raw body missing expected content; got: %.80s", body)
	}
}

// TestGzipMiddlewareSSEEndpointNotWrapped confirms that /api/events is NOT
// wrapped in gzip (it is registered without gzipMiddleware in server.go).
// We verify by checking the mux routing: /api/events uses handleEvents directly.
func TestGzipMiddlewareSSEEndpointNotWrapped(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Send a request to /api/events — it should return 503 (no socket configured),
	// NOT a gzip response.
	req := httptest.NewRequest("GET", "/api/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if enc := resp.Header.Get("Content-Encoding"); enc == "gzip" {
		t.Error("/api/events should never have Content-Encoding: gzip (SSE endpoint)")
	}
}

// TestGzipMiddlewareDoesNotBreakSessionsEndpoint runs a round-trip through the
// full server mux with Accept-Encoding: gzip, checking that /api/sessions
// returns valid JSON after decompression.
func TestGzipMiddlewareDoesNotBreakSessionsEndpoint(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	for i := range 3 {
		id := "gzip-sess-" + string(rune('a'+i))
		_ = db.UpsertSession(id, event.PlatformClaude, now.Add(time.Duration(i)*time.Minute))
	}

	srv := NewServer(db, "0")

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		defer gr.Close()
		reader = gr
	}

	var sessions []map[string]any
	if err := json.NewDecoder(reader).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions JSON: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

// TestGzipMiddlewareMetricsNotWrapped confirms /metrics does not get gzip
// encoding even when the client requests it.
func TestGzipMiddlewareMetricsNotWrapped(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if enc := resp.Header.Get("Content-Encoding"); enc == "gzip" {
		t.Error("/metrics should never have Content-Encoding: gzip")
	}
}
