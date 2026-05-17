package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	if _, err := w.Write([]byte(input)); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})
}

func TestRunInitSkipPrompts(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	withArgs(t, []string{"tokenmeter", "init", "--skip-prompts"})

	out := captureStdout(t, func() {
		if err := runInit(); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	if strings.Contains(out, "Enter monthly limit") {
		t.Fatalf("--skip-prompts should not prompt:\n%s", out)
	}
	for _, want := range []string{"Welcome to TokenMeter!", "Setting up Claude Code hooks", "Running self-diagnostic", "Setup complete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output missing %q:\n%s", want, out)
		}
	}
	settings := readSettingsJSON(t, home)
	if _, ok := settings["hooks"]; !ok {
		t.Fatalf("settings hooks missing after init: %#v", settings)
	}
}

func TestRunInitInjectsHooks(t *testing.T) {
	home := t.TempDir()
	openHomeDB(t, home)
	withArgs(t, []string{"tokenmeter", "init", "--skip-prompts"})

	_ = captureStdout(t, func() {
		if err := runInit(); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	settings := readSettingsJSON(t, home)
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("expected hooks object, got %#v", settings["hooks"])
	}
	for _, hookName := range tokenmeterHookNames {
		if _, ok := hooks[hookName]; !ok {
			t.Fatalf("expected hook %q to be configured", hookName)
		}
	}
}

func TestRunInitDetectsExistingBudgets(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	if _, err := db.InsertBudget("Claude monthly", 100, "claude"); err != nil {
		t.Fatalf("insert existing budget: %v", err)
	}
	withArgs(t, []string{"tokenmeter", "init"})
	withStdin(t, "100\nclaude\n\nn\n")

	out := captureStdout(t, func() {
		if err := runInit(); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	if !strings.Contains(out, "Budget already exists: \"Claude monthly\"") {
		t.Fatalf("expected existing budget message:\n%s", out)
	}
	budgets, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("existing budget should not be duplicated: %#v", budgets)
	}
}

func TestRunInitInteractiveBudgetCreation(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	withArgs(t, []string{"tokenmeter", "init"})
	withStdin(t, "100\nclaude\n\nn\n")

	out := captureStdout(t, func() {
		if err := runInit(); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	if !strings.Contains(out, "Budget created: \"Claude monthly\" $100") {
		t.Fatalf("expected budget created message:\n%s", out)
	}
	budgets, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(budgets) != 1 || budgets[0].Name != "Claude monthly" || budgets[0].MonthlyUSD != 100 || budgets[0].Platform != "claude" {
		t.Fatalf("unexpected budgets: %#v", budgets)
	}
}

func TestRunInitInteractiveSkipsOnEmpty(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	withArgs(t, []string{"tokenmeter", "init"})
	withStdin(t, "\n\nn\n")

	out := captureStdout(t, func() {
		if err := runInit(); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	if !strings.Contains(out, "[skipped]") {
		t.Fatalf("expected skipped steps:\n%s", out)
	}
	budgets, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(budgets) != 0 {
		t.Fatalf("empty budget prompt should not create budget: %#v", budgets)
	}
	if _, err := os.Stat(filepath.Join(home, ".tokenmeter", "webhooks.json")); !os.IsNotExist(err) {
		t.Fatalf("empty webhook prompt should not create webhooks.json: %v", err)
	}
}
