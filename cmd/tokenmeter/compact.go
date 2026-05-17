package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runCompact() error {
	full := false
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--full":
			full = true
		default:
			return fmt.Errorf("unknown compact option %q", arg)
		}
	}

	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	before, err := db.MaintenanceStats()
	if err != nil {
		return fmt.Errorf("maintenance stats: %w", err)
	}

	fmt.Println("TokenMeter compact:")
	fmt.Printf("  Before: %s (%.0f fragmentation %%)\n", formatCompactBytes(before.DBSizeBytes), before.FragmentationPct)

	if full {
		if running, _ := daemon.IsRunning(); running {
			fmt.Print("  daemon is running; VACUUM will block writes. Continue? [y/N] ")
			if !confirmCompact(os.Stdin) {
				fmt.Println("aborted")
				return fmt.Errorf("compact aborted")
			}
		}
		fmt.Println("  Running VACUUM...")
		if err := db.Vacuum(); err != nil {
			return fmt.Errorf("vacuum: %w", err)
		}
	} else {
		fmt.Println("  Running ANALYZE...")
		if err := db.Analyze(); err != nil {
			return fmt.Errorf("analyze: %w", err)
		}
	}

	after, err := db.MaintenanceStats()
	if err != nil {
		return fmt.Errorf("maintenance stats after compact: %w", err)
	}
	fmt.Printf("  After:  %s (%.0f fragmentation %%)\n", formatCompactBytes(after.DBSizeBytes), after.FragmentationPct)
	fmt.Printf("  Fragmentation: %.1f%% -> %.1f%%\n", before.FragmentationPct, after.FragmentationPct)
	if full {
		saved := before.DBSizeBytes - after.DBSizeBytes
		if saved < 0 {
			saved = 0
		}
		fmt.Printf("  Saved: %s\n", formatCompactBytes(saved))
	}
	return nil
}

func confirmCompact(f *os.File) bool {
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func formatCompactBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
	)
	switch {
	case n >= mib:
		return fmt.Sprintf("%.0f MB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.0f KB", float64(n)/kib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
