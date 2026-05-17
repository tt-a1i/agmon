package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWebGenerateToken(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	withArgs(t, []string{"tokenmeter", "web", "--generate-token"})

	out := captureStdout(t, func() {
		if err := runWeb(); err != nil {
			t.Fatalf("runWeb --generate-token: %v", err)
		}
	})

	path := filepath.Join(home, ".tokenmeter", "web-token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat web-token: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("web-token mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read web-token: %v", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		t.Fatal("generated token is empty")
	}
	if !strings.Contains(out, "Token written to") || !strings.Contains(out, "Authorization: Bearer "+token) {
		t.Fatalf("generate-token output missing token guidance:\n%s", out)
	}
}

func TestRunWebEmptyTokenFile(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "web-token"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withArgs(t, []string{"tokenmeter", "web", "--port", "0"})

	err := runWeb()
	if err == nil {
		t.Fatal("expected empty web-token error")
	}
	if !strings.Contains(err.Error(), "web-token file exists but empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveWebAuthTokenNoAuthOverridesFile(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "web-token"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	token, err := resolveWebAuthToken(webOptions{noAuth: true})
	if err != nil {
		t.Fatalf("resolve no-auth: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty when --no-auth is set", token)
	}
}
