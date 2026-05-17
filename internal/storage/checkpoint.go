package storage

import "os"

type CheckpointResult struct {
	Busy       int
	Log        int
	Truncated  bool
	DBWalBytes int64
}

func (s *DB) CheckpointTruncate() (CheckpointResult, error) {
	var result CheckpointResult
	var checkpointed int
	if err := s.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&result.Busy, &result.Log, &checkpointed); err != nil {
		return result, err
	}
	result.Truncated = result.Busy == 0

	if info, err := os.Stat(s.path + "-wal"); err == nil {
		result.DBWalBytes = info.Size()
	} else if !os.IsNotExist(err) {
		return result, err
	}
	return result, nil
}
