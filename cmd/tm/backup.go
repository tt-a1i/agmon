package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runBackup() error {
	if maybePrintCmdHelp("backup", os.Args[2:]) {
		return nil
	}
	if len(os.Args) > 3 {
		return fmt.Errorf("usage: tm backup [dest-path]")
	}

	destPath := defaultBackupPath(time.Now())
	if len(os.Args) == 3 {
		destPath = os.Args[2]
	}
	if err := confirmBackupOverwrite(destPath); err != nil {
		return err
	}

	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	origSize, backupSize, err := db.BackupTo(destPath)
	if err != nil {
		return err
	}

	fmt.Printf("Backup created: %s\n", destPath)
	fmt.Printf("Size: %s\n", formatCompactBytes(backupSize))
	if origSize > 0 {
		smallerPct := float64(origSize-backupSize) / float64(origSize) * 100
		if smallerPct < 0 {
			smallerPct = 0
		}
		fmt.Printf("Original size: %s (%.0f%% smaller after vacuum)\n", formatCompactBytes(origSize), smallerPct)
	} else {
		fmt.Printf("Original size: %s\n", formatCompactBytes(origSize))
	}
	return nil
}

func defaultBackupPath(now time.Time) string {
	return appdir.Path("backups", fmt.Sprintf("tokenmeter-%s.db", now.Format("20060102-150405")))
}

func confirmBackupOverwrite(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat backup destination: %w", err)
	}

	fmt.Printf("Backup destination exists. Overwrite? [y/N] ")
	if !confirmCompact(os.Stdin) {
		fmt.Println("aborted")
		return fmt.Errorf("backup aborted")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove existing backup: %w", err)
	}
	walPath := path + "-wal"
	if err := os.Remove(walPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing backup wal: %w", err)
	}
	shmPath := path + "-shm"
	if err := os.Remove(shmPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing backup shm: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	return nil
}
