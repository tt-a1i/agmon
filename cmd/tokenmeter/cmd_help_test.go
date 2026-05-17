package main

import (
	"strings"
	"testing"
)

func TestPrintCmdHelpForEachCommand(t *testing.T) {
	for name, help := range cmdHelps {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := printCmdHelp(name); err != nil {
					t.Fatalf("printCmdHelp: %v", err)
				}
			})
			for _, want := range []string{"Usage: " + help.usage, help.description} {
				if !strings.Contains(out, want) {
					t.Fatalf("command help for %q missing %q:\n%s", name, want, out)
				}
			}
		})
	}
}

func TestRunExportWithHelpFlag(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "export", "--help"})

	out := captureStdout(t, func() {
		if err := runExport(); err != nil {
			t.Fatalf("runExport: %v", err)
		}
	})

	for _, want := range []string{
		"Usage: tokenmeter export [options]",
		"Export session/token data as CSV or JSON",
		"--range RANGE",
		"See also: tokenmeter compare, tokenmeter share",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("export help missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompareWithHelpFlag(t *testing.T) {
	withArgs(t, []string{"tokenmeter", "compare", "-h"})

	out := captureStdout(t, func() {
		if err := runCompare(); err != nil {
			t.Fatalf("runCompare: %v", err)
		}
	})

	for _, want := range []string{
		"Usage: tokenmeter compare <sessionA> <sessionB> [options]",
		"Compare two sessions",
		"--format FORMAT",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compare help missing %q:\n%s", want, out)
		}
	}
}

func TestRunUnknownCommandSuggestsHelp(t *testing.T) {
	msg := unknownCommandHelpMessage("bogus")
	if !strings.Contains(msg, "unknown command: bogus") {
		t.Fatalf("missing unknown command detail: %q", msg)
	}
	if !strings.Contains(msg, "Run 'tokenmeter help'") {
		t.Fatalf("missing help suggestion: %q", msg)
	}
}

func TestCmdHelpCoversTopLevelHelpCommands(t *testing.T) {
	for _, section := range helpSections {
		for _, command := range section.commands {
			name := strings.Fields(command.name)[0]
			if _, ok := cmdHelps[name]; !ok {
				t.Fatalf("cmdHelps missing %q from top-level help", name)
			}
		}
	}
}
