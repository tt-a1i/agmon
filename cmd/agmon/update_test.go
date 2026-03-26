package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestFindAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "agmon_darwin_amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_amd64"},
		{Name: "agmon_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64"},
		{Name: "agmon_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64"},
		{Name: "agmon_windows_amd64.zip", BrowserDownloadURL: "https://example.com/windows_amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
	}

	tests := []struct {
		goos, goarch string
		wantName     string
	}{
		{"darwin", "arm64", "agmon_darwin_arm64.tar.gz"},
		{"linux", "amd64", "agmon_linux_amd64.tar.gz"},
		{"windows", "amd64", "agmon_windows_amd64.zip"},
		{"freebsd", "amd64", ""},
	}
	for _, tt := range tests {
		got := findAsset(assets, tt.goos, tt.goarch)
		if tt.wantName == "" {
			if got != nil {
				t.Errorf("findAsset(%s, %s) = %q, want nil", tt.goos, tt.goarch, got.Name)
			}
			continue
		}
		if got == nil || got.Name != tt.wantName {
			name := ""
			if got != nil {
				name = got.Name
			}
			t.Errorf("findAsset(%s, %s) = %q, want %q", tt.goos, tt.goarch, name, tt.wantName)
		}
	}

	// checksums lookup
	got := findAsset(assets, "", "")
	if got == nil || got.Name != "checksums.txt" {
		t.Errorf("findAsset('', '') should return checksums.txt")
	}
}

func TestParseChecksums(t *testing.T) {
	content := "abc123  agmon_darwin_arm64.tar.gz\ndef456  agmon_linux_amd64.tar.gz\n"
	m := parseChecksums(content)
	if m["agmon_darwin_arm64.tar.gz"] != "abc123" {
		t.Errorf("got %q, want abc123", m["agmon_darwin_arm64.tar.gz"])
	}
	if m["agmon_linux_amd64.tar.gz"] != "def456" {
		t.Errorf("got %q, want def456", m["agmon_linux_amd64.tar.gz"])
	}
}

func TestSha256sum(t *testing.T) {
	// sha256 of empty data
	got := sha256sum([]byte{})
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("sha256sum(empty) = %q, want %q", got, want)
	}
}

func TestExtractTarGz(t *testing.T) {
	binContent := []byte("fake-agmon-binary")
	data := buildTarGz(t, "agmon", binContent)

	got, err := extractTarGz(data)
	if err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if !bytes.Equal(got, binContent) {
		t.Errorf("got %q, want %q", got, binContent)
	}
}

func TestExtractTarGzMissing(t *testing.T) {
	data := buildTarGz(t, "README.md", []byte("readme"))
	_, err := extractTarGz(data)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestExtractZip(t *testing.T) {
	binContent := []byte("fake-agmon-binary")
	data := buildZip(t, "agmon.exe", binContent)

	got, err := extractZip(data)
	if err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if !bytes.Equal(got, binContent) {
		t.Errorf("got %q, want %q", got, binContent)
	}
}

func TestExtractZipMissing(t *testing.T) {
	data := buildZip(t, "README.md", []byte("readme"))
	_, err := extractZip(data)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agmon")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	newBin := []byte("new-binary-content")
	if err := replaceBinary(target, newBin); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBin) {
		t.Errorf("got %q, want %q", got, newBin)
	}
}

// --- helpers ---

func buildTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0o755})
	tw.Write(content)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func buildZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write(content)
	zw.Close()
	return buf.Bytes()
}
