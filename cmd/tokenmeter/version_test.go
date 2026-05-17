package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunVersionTextDefault(t *testing.T) {
	restore := setVersionForTest("0.7.0")
	defer restore()

	var out bytes.Buffer
	if err := runVersionWithDeps([]string{"version"}, &out); err != nil {
		t.Fatalf("runVersion: %v", err)
	}

	if got, want := strings.TrimSpace(out.String()), "tokenmeter v0.7.0"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRunVersionCheckLatest(t *testing.T) {
	restoreVersion := setVersionForTest("0.7.0")
	defer restoreVersion()
	restoreAPI := setReleaseAPIServer(t, `{"tag_name":"v0.8.0","published_at":"2026-05-15T10:00:00Z"}`)
	defer restoreAPI()

	var out bytes.Buffer
	if err := runVersionWithDeps([]string{"version", "--check"}, &out); err != nil {
		t.Fatalf("runVersion --check: %v", err)
	}

	for _, want := range []string{"TokenMeter v0.7.0 (current)", "Latest: v0.8.0", "Update:", "To update: tokenmeter update"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("version --check output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunVersionCheckJSON(t *testing.T) {
	restoreVersion := setVersionForTest("0.7.0")
	defer restoreVersion()
	restoreAPI := setReleaseAPIServer(t, `{"tag_name":"v0.8.0","published_at":"2026-05-15T10:00:00Z"}`)
	defer restoreAPI()

	var out bytes.Buffer
	if err := runVersionWithDeps([]string{"version", "--check", "--json"}, &out); err != nil {
		t.Fatalf("runVersion --check --json: %v", err)
	}

	var info VersionInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if info.Current != "v0.7.0" || info.Latest != "v0.8.0" || !info.IsNewerAvailable {
		t.Fatalf("version info = %#v", info)
	}
}

func TestRunVersionCheckCurrent(t *testing.T) {
	restoreVersion := setVersionForTest("0.8.0")
	defer restoreVersion()
	restoreAPI := setReleaseAPIServer(t, `{"tag_name":"v0.8.0","published_at":"2026-05-15T10:00:00Z"}`)
	defer restoreAPI()

	var out bytes.Buffer
	if err := runVersionWithDeps([]string{"version", "--check"}, &out); err != nil {
		t.Fatalf("runVersion --check current: %v", err)
	}
	if !strings.Contains(out.String(), "TokenMeter v0.8.0 (current, latest)") || !strings.Contains(out.String(), "You're up to date.") {
		t.Fatalf("current version output:\n%s", out.String())
	}
}

func TestRunVersionCheckOffline(t *testing.T) {
	restoreVersion := setVersionForTest("0.7.0")
	defer restoreVersion()
	restoreFetch := setFetchLatestReleaseForTest(func() (*ghRelease, error) {
		return nil, errors.New("offline")
	})
	defer restoreFetch()

	var out bytes.Buffer
	if err := runVersionWithDeps([]string{"version", "--check"}, &out); err != nil {
		t.Fatalf("offline check should not fail: %v", err)
	}
	if !strings.Contains(out.String(), "TokenMeter v0.7.0") || !strings.Contains(out.String(), "Update check failed: offline") {
		t.Fatalf("offline output:\n%s", out.String())
	}
}

func TestIsNewerVersionSemver(t *testing.T) {
	tests := []struct {
		remote string
		local  string
		want   bool
	}{
		{"v0.8.0", "v0.7.5", true},
		{"v0.8.1", "v0.8.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.8.0", "v0.8.0", false},
		{"v0.7.9", "v0.8.0", false},
		{"dev", "v0.8.0", false},
	}
	for _, tt := range tests {
		if got := isNewerVersion(tt.remote, tt.local); got != tt.want {
			t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tt.remote, tt.local, got, tt.want)
		}
	}
}

func setVersionForTest(v string) func() {
	prev := version
	version = v
	return func() { version = prev }
}

func setReleaseAPIServer(t *testing.T, body string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	prev := releaseAPI
	releaseAPI = srv.URL
	return func() {
		releaseAPI = prev
		srv.Close()
	}
}

func setFetchLatestReleaseForTest(fn func() (*ghRelease, error)) func() {
	prev := fetchLatestReleaseFunc
	fetchLatestReleaseFunc = fn
	return func() { fetchLatestReleaseFunc = prev }
}
