package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

const (
	autoBackupInitialDelay = 30 * time.Minute
	autoBackupInterval     = 24 * time.Hour
	autoBackupMinAge       = 23 * time.Hour
	autoBackupKeep         = 4
)

func (d *Daemon) autoBackupLoop() {
	timer := time.NewTimer(autoBackupInitialDelay)
	defer timer.Stop()
	select {
	case <-d.done:
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(autoBackupInterval)
	defer ticker.Stop()
	for {
		if err := d.runAutoBackup(); err != nil {
			log.Printf("auto-backup: %v", err)
		}
		select {
		case <-d.done:
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) runAutoBackup() error {
	dir := appdir.Path("backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	last := mostRecentAutoBackup(dir)
	if !last.IsZero() && time.Since(last) < autoBackupMinAge {
		return nil
	}

	dest := filepath.Join(dir, fmt.Sprintf("auto-%s.db", time.Now().Format("20060102-150405")))
	if _, _, err := d.db.BackupTo(dest); err != nil {
		return err
	}
	if err := rotateBackups(dir, "auto-", autoBackupKeep); err != nil {
		return err
	}
	log.Printf("auto-backup created: %s", filepath.Base(dest))
	return nil
}
