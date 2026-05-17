package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BackupTo writes a compact SQLite snapshot to destPath using VACUUM INTO.
// It checkpoints WAL first so the reported source size reflects recent writes.
func (s *DB) BackupTo(destPath string) (origSize, backupSize int64, err error) {
	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		return 0, 0, fmt.Errorf("backup destination is required")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, 0, fmt.Errorf("create backup dir: %w", err)
	}

	sourcePath, err := s.mainDBPath()
	if err != nil {
		return 0, 0, err
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		return 0, 0, fmt.Errorf("checkpoint wal: %w", err)
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return 0, 0, fmt.Errorf("stat source db: %w", err)
	}
	origSize = sourceInfo.Size()

	if _, err := os.Stat(destPath); err == nil {
		return 0, 0, fmt.Errorf("backup destination already exists: %s", destPath)
	} else if !os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("stat backup destination: %w", err)
	}

	if _, err := s.db.Exec(`VACUUM INTO ?`, destPath); err != nil {
		return 0, 0, fmt.Errorf("vacuum into backup: %w", err)
	}
	backupInfo, err := os.Stat(destPath)
	if err != nil {
		return 0, 0, fmt.Errorf("stat backup: %w", err)
	}
	return origSize, backupInfo.Size(), nil
}

func (s *DB) mainDBPath() (string, error) {
	rows, err := s.db.Query(`PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("database list: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			seq  int
			name string
			file string
		)
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", fmt.Errorf("scan database list: %w", err)
		}
		if name == "main" {
			if file == "" {
				return "", fmt.Errorf("main database has no filesystem path")
			}
			return file, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("database list rows: %w", err)
	}
	return "", fmt.Errorf("main database not found")
}

// ValidateSQLiteBackup checks that path is a readable SQLite database with the
// TokenMeter sessions table before restore overwrites the active database.
func ValidateSQLiteBackup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backup source: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("backup source is a directory")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()

	var quickCheck string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&quickCheck); err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	if quickCheck != "ok" {
		return fmt.Errorf("quick_check: %s", quickCheck)
	}

	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'sessions'`).Scan(&tables); err != nil {
		return fmt.Errorf("check sessions table: %w", err)
	}
	if tables == 0 {
		return fmt.Errorf("sessions table missing")
	}
	return nil
}
