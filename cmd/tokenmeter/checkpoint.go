package main

import (
	"fmt"
	"os"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runCheckpoint() error {
	if maybePrintCmdHelp("checkpoint", os.Args[2:]) {
		return nil
	}
	if len(os.Args) > 2 {
		return fmt.Errorf("usage: tokenmeter checkpoint")
	}

	dbPath := storage.DefaultDBPath()
	before := fileSize(dbPath + "-wal")
	if running, _ := daemon.IsRunning(); running {
		fmt.Println("daemon is writing; checkpoint may have pending frames")
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	fmt.Println("WAL checkpoint:")
	fmt.Printf("  Before: %s %s\n", dbPath+"-wal", formatBytes(before))
	fmt.Println("  Running TRUNCATE...")
	result, err := db.CheckpointTruncate()
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	after := result.DBWalBytes
	fmt.Printf("  After:  %s %s\n", dbPath+"-wal", formatBytes(after))
	fmt.Printf("  Reclaimed %s\n", formatBytes(before-after))
	return nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
