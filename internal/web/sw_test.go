package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readStaticAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("static", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestSWHasVersionedCacheNames(t *testing.T) {
	sw := readStaticAsset(t, "sw.js")
	for _, needle := range []string{"tm-v2-static", "tm-v2-api"} {
		if !strings.Contains(sw, needle) {
			t.Errorf("sw.js missing versioned cache name %q", needle)
		}
	}
}

func TestSWSkipsAPIEvents(t *testing.T) {
	sw := readStaticAsset(t, "sw.js")
	if !strings.Contains(sw, "/api/events") {
		t.Error("sw.js must explicitly skip /api/events so SSE stream passes through")
	}
}

func TestSWHandlesActivateCleanup(t *testing.T) {
	sw := readStaticAsset(t, "sw.js")
	if !strings.Contains(sw, "caches.delete(k)") {
		t.Error("sw.js activate must delete stale caches via caches.delete(k)")
	}
	if !strings.Contains(sw, "!k.startsWith(CACHE_VERSION)") {
		t.Error("sw.js activate cleanup must filter by CACHE_VERSION prefix")
	}
}

func TestSWHasSkipWaitingMessageHandler(t *testing.T) {
	sw := readStaticAsset(t, "sw.js")
	if !strings.Contains(sw, "SKIP_WAITING") {
		t.Error("sw.js must accept SKIP_WAITING message for in-page update prompts")
	}
}

func TestManifestHasShortcuts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("static", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var m struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		Shortcuts []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"shortcuts"`
		Icons []struct {
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json is not valid JSON: %v", err)
	}
	if m.Name == "" {
		t.Error("manifest.name is required")
	}
	if len(m.Shortcuts) < 3 {
		t.Errorf("expected >=3 shortcuts for PWA quick actions, got %d", len(m.Shortcuts))
	}
	for i, s := range m.Shortcuts {
		if s.Name == "" || s.URL == "" {
			t.Errorf("shortcut[%d] missing name/url: %+v", i, s)
		}
	}
	hasMaskable := false
	for _, ic := range m.Icons {
		if strings.Contains(ic.Purpose, "maskable") {
			hasMaskable = true
			break
		}
	}
	if !hasMaskable {
		t.Error("manifest.icons must include a maskable variant for desktop PWA install")
	}
}
