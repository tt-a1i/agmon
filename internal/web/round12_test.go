package web

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteInternalErrorContract verifies the 500 response shape: JSON body
// `{"error":"internal server error"}` and Content-Type set, without leaking
// the underlying error to the client.
func TestWriteInternalErrorContract(t *testing.T) {
	w := httptest.NewRecorder()
	writeInternalError(w, errors.New("super secret SQL detail with table foo_bar"))

	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", w.Header().Get("Content-Type"))
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "internal server error" {
		t.Errorf("error message = %q, want generic 'internal server error'", body["error"])
	}
	// Body MUST NOT contain the raw error.
	if strings.Contains(w.Body.String(), "foo_bar") {
		t.Errorf("response leaked internal error detail: %s", w.Body.String())
	}
}
