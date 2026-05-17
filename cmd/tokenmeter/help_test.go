package main

import (
	"strings"
	"testing"
)

func TestPrintHelpHasAllSections(t *testing.T) {
	out := captureStdout(t, printHelp)
	for _, want := range []string{
		"▎Setup & installation",
		"▎Run modes",
		"▎Daily commands",
		"▎Analysis",
		"▎Maintenance",
		"▎Configuration",
		"▎Examples",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing section %q:\n%s", want, out)
		}
	}
}

func TestPrintHelpContainsAllCommands(t *testing.T) {
	out := captureStdout(t, printHelp)
	for _, cmd := range []string{
		"daemon",
		"reload",
		"emit",
		"setup",
		"init",
		"uninstall",
		"status",
		"report",
		"share",
		"cost",
		"export",
		"compare",
		"search",
		"budget",
		"webhook",
		"analyze",
		"watch",
		"top",
		"healthcheck",
		"logs",
		"checkpoint",
		"completion",
		"backup",
		"restore",
		"doctor",
		"compact",
		"clean",
		"tag",
		"web",
		"update",
		"version",
		"help",
	} {
		if !helpContainsCommand(out, cmd) {
			t.Fatalf("help missing command %q:\n%s", cmd, out)
		}
	}
}

func TestPrintHelpCommandsAreAligned(t *testing.T) {
	out := captureStdout(t, printHelp)
	lines := helpCommandLines(out)
	if len(lines) == 0 {
		t.Fatalf("no command lines found:\n%s", out)
	}
	descColumn := -1
	for _, line := range lines {
		column := helpDescColumn(line)
		if column < 0 {
			t.Fatalf("could not find description column in %q", line)
		}
		if descColumn < 0 {
			descColumn = column
			continue
		}
		if column != descColumn {
			t.Fatalf("description column mismatch: got %d want %d in %q\n%s", column, descColumn, line, out)
		}
	}
}

func TestPrintHelpStableAcrossRuns(t *testing.T) {
	first := captureStdout(t, printHelp)
	second := captureStdout(t, printHelp)
	if first != second {
		t.Fatalf("help output is not deterministic\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func helpContainsCommand(out, cmd string) bool {
	prefix := "  " + cmd
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func helpCommandLines(out string) []string {
	var lines []string
	inCommands := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "▎Examples") {
			break
		}
		if strings.HasPrefix(line, "▎") {
			inCommands = true
			continue
		}
		if inCommands && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func helpDescColumn(line string) int {
	trimmed := strings.TrimPrefix(line, "  ")
	for i := 0; i < len(trimmed)-1; i++ {
		if trimmed[i] == ' ' && trimmed[i+1] == ' ' {
			j := i + 2
			for j < len(trimmed) && trimmed[j] == ' ' {
				j++
			}
			if j < len(trimmed) {
				return 2 + j
			}
			return -1
		}
	}
	return -1
}
