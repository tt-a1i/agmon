package storage

type StatsRow struct {
	DBSizeBytes      int64
	FreelistPages    int64
	IndexCount       int
	FragmentationPct float64
}

func (s *DB) Vacuum() error {
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return err
	}
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

func (s *DB) Optimize() error {
	_, err := s.db.Exec(`PRAGMA optimize`)
	return err
}

func (s *DB) MaintenanceStats() (StatsRow, error) {
	var pageSize, pageCount, freelistPages int64
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return StatsRow{}, err
	}
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return StatsRow{}, err
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freelistPages); err != nil {
		return StatsRow{}, err
	}

	var indexCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index'`).Scan(&indexCount); err != nil {
		return StatsRow{}, err
	}

	fragmentationPct := 0.0
	if pageCount > 0 {
		fragmentationPct = float64(freelistPages) / float64(pageCount) * 100
	}
	return StatsRow{
		DBSizeBytes:      pageSize * pageCount,
		FreelistPages:    freelistPages,
		IndexCount:       indexCount,
		FragmentationPct: fragmentationPct,
	}, nil
}
