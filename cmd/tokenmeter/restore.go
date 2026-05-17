package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func runRestore() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: tokenmeter restore <source-path>")
	}
	sourcePath := os.Args[2]
	if err := storage.ValidateSQLiteBackup(sourcePath); err != nil {
		return fmt.Errorf("invalid backup source: %w", err)
	}
	if err := stopDaemonForRestore(); err != nil {
		return err
	}

	dbPath := storage.DefaultDBPath()
	preBackupPath, err := preRestoreBackup(dbPath, time.Now())
	if err != nil {
		return err
	}
	if preBackupPath != "" {
		fmt.Printf("Pre-restore backup: %s\n", preBackupPath)
	}

	if err := replaceDBFile(sourcePath, dbPath); err != nil {
		return err
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("stat restored db: %w", err)
	}

	fmt.Printf("Restored from: %s\n", sourcePath)
	fmt.Printf("New DB size: %s\n", formatCompactBytes(info.Size()))
	fmt.Println("Run 'tokenmeter doctor' to verify.")
	return nil
}

func stopDaemonForRestore() error {
	running, pid := daemon.IsRunning()
	if !running {
		return nil
	}
	fmt.Print("daemon is running. Stop daemon first? [y/N] ")
	if !confirmCompact(os.Stdin) {
		fmt.Println("aborted")
		return fmt.Errorf("restore aborted")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon %d: %w", pid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running, _ := daemon.IsRunning()
		if !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon %d did not stop", pid)
}

func preRestoreBackup(dbPath string, now time.Time) (string, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("stat current db: %w", err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open current db: %w", err)
	}
	defer db.Close()

	destPath := appdir.Path("backups", fmt.Sprintf("pre-restore-%s.db", now.Format("20060102-150405")))
	if _, _, err := db.BackupTo(destPath); err != nil {
		return "", fmt.Errorf("pre-restore backup: %w", err)
	}
	return destPath, nil
}

func replaceDBFile(sourcePath, dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	if err := copyFile(sourcePath, dbPath); err != nil {
		return fmt.Errorf("copy backup into place: %w", err)
	}
	return nil
}

func copyFile(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Sync()
}
