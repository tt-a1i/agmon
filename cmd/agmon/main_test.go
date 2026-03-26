package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureTUILoggingRedirectsStandardLoggerToFile(t *testing.T) {
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})

	logPath := filepath.Join(t.TempDir(), "agmon.log")
	restore, err := configureTUILogging(logPath)
	if err != nil {
		t.Fatalf("configure tui logging: %v", err)
	}
	if log.Writer() == prevWriter || log.Writer() == os.Stderr {
		t.Fatal("expected logger output to be redirected away from stderr during TUI mode")
	}

	log.Print("background daemon log")

	if err := restore(); err != nil {
		t.Fatalf("restore logger: %v", err)
	}
	if log.Writer() != prevWriter {
		t.Fatal("expected logger output to be restored to its previous writer")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "background daemon log") {
		t.Fatalf("expected redirected log file to contain test message, got %q", string(data))
	}
}
