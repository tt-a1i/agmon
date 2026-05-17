package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoAuthByDefault(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
}

func TestRequiresBearerWhenConfigured(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="tokenmeter"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestAcceptsCorrectBearer(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
	}
}

func TestRejectsWrongBearer(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", w.Code)
	}
}

func TestStaticFilesAreUnauthenticated(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "TokenMeter") {
		t.Fatalf("static response does not look like index.html")
	}
}

func TestMetricsRequiresBearerWhenAuth(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", w.Code)
	}
}

func TestEventsAcceptsTokenParam(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithAuthToken("secret"))
	srv.eventSockPath = ""

	req := httptest.NewRequest(http.MethodGet, "/api/events?token=secret", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("expected token param to pass auth, got 401")
	}
}
