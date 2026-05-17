package daemon

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

func TestSetupLogFileCreatesFile(t *testing.T) {
	base := setWebhookTestHome(t)

	cleanup, err := SetupLogFile()
	if err != nil {
		t.Fatalf("SetupLogFile: %v", err)
	}
	log.Print("daemon-log-file-test")
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(base, "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon log: %v", err)
	}
	if !strings.Contains(string(data), "daemon-log-file-test") {
		t.Fatalf("daemon log missing message: %q", string(data))
	}
}

func TestLogRotationAtSizeLimit(t *testing.T) {
	setWebhookTestHome(t)
	restore := setLogRotationForTest(t, 80, 5)

	cleanup, err := SetupLogFile()
	if err != nil {
		t.Fatalf("SetupLogFile: %v", err)
	}
	log.Print(strings.Repeat("a", 120))
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	restore()

	if _, err := os.Stat(appdir.Path("daemon.log.1")); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
}

func TestLogRotationKeepsMaxBackups(t *testing.T) {
	setWebhookTestHome(t)
	restore := setLogRotationForTest(t, 40, 5)

	cleanup, err := SetupLogFile()
	if err != nil {
		t.Fatalf("SetupLogFile: %v", err)
	}
	for i := 0; i < 8; i++ {
		log.Print(strings.Repeat("b", 80))
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	restore()

	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(appdir.Path("daemon.log." + string(rune('0'+i)))); err != nil {
			t.Fatalf("backup %d missing: %v", i, err)
		}
	}
	if _, err := os.Stat(appdir.Path("daemon.log.6")); !os.IsNotExist(err) {
		t.Fatalf("backup 6 should not exist, stat err=%v", err)
	}
}

func setLogRotationForTest(t *testing.T, maxBytes int64, maxBackups int) func() {
	t.Helper()
	prevBytes := daemonLogMaxBytes
	prevBackups := daemonLogMaxBackups
	daemonLogMaxBytes = maxBytes
	daemonLogMaxBackups = maxBackups
	return func() {
		daemonLogMaxBytes = prevBytes
		daemonLogMaxBackups = prevBackups
	}
}
