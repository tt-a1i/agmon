package main

import (
	"path/filepath"
	"testing"
)

func TestRunTUIDefaultsToCurrentWorkspace(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "project")
	opts, err := parseTUIOptions(nil, func() (string, error) { return cwd, nil })
	if err != nil {
		t.Fatalf("parse tui options: %v", err)
	}
	if opts.workspace != cwd {
		t.Fatalf("workspace = %q, want %q", opts.workspace, cwd)
	}
	if !opts.workspaceFilter {
		t.Fatal("workspaceFilter should default to true")
	}
}

func TestRunTUIRespectsAllFlag(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "project")
	opts, err := parseTUIOptions([]string{"--all"}, func() (string, error) { return cwd, nil })
	if err != nil {
		t.Fatalf("parse tui options: %v", err)
	}
	if opts.workspaceFilter {
		t.Fatal("--all should disable workspace filtering")
	}
	if opts.workspace != "" {
		t.Fatalf("--all workspace = %q, want empty", opts.workspace)
	}
}

func TestRunTUIRespectsWorkspaceFlag(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom")
	opts, err := parseTUIOptions([]string{"--workspace", custom}, func() (string, error) {
		return filepath.Join(t.TempDir(), "cwd"), nil
	})
	if err != nil {
		t.Fatalf("parse tui options: %v", err)
	}
	if opts.workspace != custom {
		t.Fatalf("workspace = %q, want %q", opts.workspace, custom)
	}
	if !opts.workspaceFilter {
		t.Fatal("--workspace should enable workspace filtering")
	}
}
