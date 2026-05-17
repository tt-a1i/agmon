package main

import (
	"strings"
	"testing"
)

func TestRunCompletionBash(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "completion", "bash"})

	out := captureStdout(t, func() {
		if err := runCompletion(); err != nil {
			t.Fatalf("runCompletion: %v", err)
		}
	})

	for _, want := range []string{"_tm()", "complete -F _tm tm", "budget)", "completion)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bash completion missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompletionZsh(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "completion", "zsh"})

	out := captureStdout(t, func() {
		if err := runCompletion(); err != nil {
			t.Fatalf("runCompletion: %v", err)
		}
	})

	for _, want := range []string{"#compdef tm", "_tm \"$@\"", "budget) _values"} {
		if !strings.Contains(out, want) {
			t.Fatalf("zsh completion missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompletionFish(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "completion", "fish"})

	out := captureStdout(t, func() {
		if err := runCompletion(); err != nil {
			t.Fatalf("runCompletion: %v", err)
		}
	})

	for _, want := range []string{"complete -c tm", "__fish_use_subcommand", "__fish_seen_subcommand_from budget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fish completion missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompletionUnknownShell(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "completion", "powershell"})

	err := runCompletion()
	if err == nil {
		t.Fatal("expected unknown shell error")
	}
	if !strings.Contains(err.Error(), "unknown shell: powershell") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCompletionListsAllSubcommands(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "completion", "bash"})

	out := captureStdout(t, func() {
		if err := runCompletion(); err != nil {
			t.Fatalf("runCompletion: %v", err)
		}
	})

	for _, cmd := range completionSubcommands {
		if !completionScriptContainsWord(out, cmd) {
			t.Fatalf("bash completion missing subcommand %q:\n%s", cmd, out)
		}
	}
}

func completionScriptContainsWord(script, word string) bool {
	for _, field := range strings.FieldsFunc(script, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '"' || r == '\''
	}) {
		if field == word {
			return true
		}
	}
	return false
}
