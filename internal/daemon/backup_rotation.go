package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type backupFileInfo struct {
	path    string
	modTime time.Time
}

func rotateBackups(dir, prefix string, keep int) error {
	if keep < 0 {
		keep = 0
	}
	backups, err := listBackupFiles(dir, prefix)
	if err != nil {
		return err
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})
	for i := keep; i < len(backups); i++ {
		if err := os.Remove(backups[i].path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func mostRecentAutoBackup(dir string) time.Time {
	backups, err := listBackupFiles(dir, "auto-")
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, backup := range backups {
		if backup.modTime.After(latest) {
			latest = backup.modTime
		}
	}
	return latest
}

func listBackupFiles(dir, prefix string) ([]backupFileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	backups := make([]backupFileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || filepath.Ext(name) != ".db" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		backups = append(backups, backupFileInfo{
			path:    filepath.Join(dir, name),
			modTime: info.ModTime(),
		})
	}
	return backups, nil
}
