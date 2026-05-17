package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchLatestReleaseDecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v9.9.9",
			"assets": [
				{"name": "tokenmeter_darwin_arm64.tar.gz", "browser_download_url": "https://example.com/d"},
				{"name": "checksums.txt", "browser_download_url": "https://example.com/c"}
			]
		}`))
	}))
	defer srv.Close()

	orig := releaseAPI
	releaseAPI = srv.URL
	t.Cleanup(func() { releaseAPI = orig })

	rel, err := fetchLatestRelease()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if rel.TagName != "v9.9.9" {
		t.Errorf("tag = %q, want v9.9.9", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("assets = %d, want 2", len(rel.Assets))
	}
}

func TestFetchLatestReleaseHandlesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	orig := releaseAPI
	releaseAPI = srv.URL
	t.Cleanup(func() { releaseAPI = orig })

	_, err := fetchLatestRelease()
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestFetchChecksumsParsesShaLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("aaaa  file1.tar.gz\nbbbb  file2.zip\n"))
	}))
	defer srv.Close()

	got, err := fetchChecksums(srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got["file1.tar.gz"] != "aaaa" {
		t.Errorf("file1 checksum = %q, want aaaa", got["file1.tar.gz"])
	}
	if got["file2.zip"] != "bbbb" {
		t.Errorf("file2 checksum = %q, want bbbb", got["file2.zip"])
	}
}

func TestFetchChecksumsHandles404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := fetchChecksums(srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestDownloadAssetReturnsBody(t *testing.T) {
	body := []byte("fake binary content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := downloadAsset(srv.URL)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch")
	}
}

func TestDownloadAssetHandles500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := downloadAsset(srv.URL)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
