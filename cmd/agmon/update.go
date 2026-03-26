package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const releaseAPI = "https://api.github.com/repos/tt-a1i/agmon/releases/latest"

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpdate() {
	fmt.Printf("agmon v%s — checking for updates...\n", version)

	rel, err := fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to check for updates: %v\n", err)
		os.Exit(1)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest == version && version != "dev" {
		fmt.Println("Already up to date.")
		return
	}

	fmt.Printf("New version available: v%s → v%s\n", version, latest)

	asset := findAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		fmt.Fprintf(os.Stderr, "No binary found for %s/%s\n", runtime.GOOS, runtime.GOARCH)
		os.Exit(1)
	}

	checksums := findAsset(rel.Assets, "", "")
	if checksums == nil {
		fmt.Fprintf(os.Stderr, "checksums.txt not found in release, aborting\n")
		os.Exit(1)
	}
	checksumMap, err := fetchChecksums(checksums.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch checksums: %v\n", err)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot locate current binary: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	fmt.Printf("Downloading %s...\n", asset.Name)
	archiveData, err := downloadAsset(asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}

	expected, ok := checksumMap[asset.Name]
	if !ok {
		fmt.Fprintf(os.Stderr, "No checksum found for %s, aborting\n", asset.Name)
		os.Exit(1)
	}
	actual := sha256sum(archiveData)
	if actual != expected {
		fmt.Fprintf(os.Stderr, "Checksum mismatch: expected %s, got %s\n", expected, actual)
		os.Exit(1)
	}
	fmt.Println("Checksum verified.")

	var bin []byte
	if strings.HasSuffix(asset.Name, ".tar.gz") {
		bin, err = extractTarGz(archiveData)
	} else if strings.HasSuffix(asset.Name, ".zip") {
		bin, err = extractZip(archiveData)
	} else {
		err = fmt.Errorf("unknown archive format: %s", asset.Name)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Extract failed: %v\n", err)
		os.Exit(1)
	}

	if err := replaceBinary(exe, bin); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Updated to v%s\n", latest)
}

func fetchLatestRelease() (*ghRelease, error) {
	resp, err := http.Get(releaseAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func findAsset(assets []ghAsset, goos, goarch string) *ghAsset {
	if goos == "" && goarch == "" {
		for i := range assets {
			if assets[i].Name == "checksums.txt" {
				return &assets[i]
			}
		}
		return nil
	}
	suffix := fmt.Sprintf("_%s_%s.", goos, goarch)
	for i := range assets {
		if strings.Contains(assets[i].Name, suffix) {
			return &assets[i]
		}
	}
	return nil
}

func fetchChecksums(url string) (map[string]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download checksums returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseChecksums(string(body)), nil
}

func parseChecksums(content string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			m[parts[1]] = parts[0]
		}
	}
	return m
}

func sha256sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func downloadAsset(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extractTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := filepath.Base(hdr.Name)
		if name == "agmon" || name == "agmon.exe" {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary not found in archive")
}

func extractZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if name == "agmon" || name == "agmon.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("binary not found in archive")
}

func replaceBinary(target string, newBin []byte) error {
	dir := filepath.Dir(target)

	// Write new binary to a temp file in the same directory.
	tmp, err := os.CreateTemp(dir, ".agmon-update-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// On Windows, a running .exe cannot be overwritten but CAN be renamed.
	// Move the old binary out of the way first, then move the new one in.
	if runtime.GOOS == "windows" {
		oldPath := target + ".old"
		os.Remove(oldPath) // clean up from previous update
		if err := os.Rename(target, oldPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("move old binary: %w", err)
		}
		if err := os.Rename(tmpPath, target); err != nil {
			// Try to restore the old binary.
			os.Rename(oldPath, target)
			os.Remove(tmpPath)
			return fmt.Errorf("replace binary: %w", err)
		}
		// Leave .old file; it will be cleaned up on next update.
		return nil
	}

	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}
