package storage

import (
	"database/sql"
	"hash/fnv"
	"strings"
	"time"
	"unicode"
)

const (
	searchIndexBackfillKey     = "search_index_backfill"
	searchIndexBackfillVersion = 1
)

func (s *DB) createSearchIndex() error {
	_, err := s.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
			session_id UNINDEXED,
			kind UNINDEXED,
			content,
			ts UNINDEXED,
			tokenize='porter unicode61'
		)
	`)
	return err
}

// IndexToolCallContent updates the FTS rows for a tool call's params and result.
func (s *DB) IndexToolCallContent(sessionID, callID string, params, result string, ts time.Time) error {
	if s.searchFallbackLike {
		return nil
	}
	tsStr := formatStorageTime(ts)
	if strings.TrimSpace(params) != "" {
		if err := insertSearchIndexRow(s.db, searchIndexRowID("tool_param", callID), sessionID, "tool_param", params, tsStr); err != nil {
			return err
		}
	}
	if strings.TrimSpace(result) != "" {
		if err := insertSearchIndexRow(s.db, searchIndexRowID("tool_result", callID), sessionID, "tool_result", result, tsStr); err != nil {
			return err
		}
	}
	return nil
}

// IndexFileChange adds or replaces the FTS row for a file path change.
func (s *DB) IndexFileChange(sessionID, callID string, path string, ts time.Time) error {
	if s.searchFallbackLike || strings.TrimSpace(path) == "" {
		return nil
	}
	return insertSearchIndexRow(s.db, searchIndexRowID("file", sessionID, callID), sessionID, "file", path, formatStorageTime(ts))
}

// DeindexSession removes all search index rows for a session.
func (s *DB) DeindexSession(sessionID string) error {
	if s.searchFallbackLike {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM search_index WHERE session_id = ?`, sessionID)
	return err
}

func insertSearchIndexRow(exec dailyCostCacheExecer, rowID int64, sessionID, kind, content, ts string) error {
	_, err := exec.Exec(`
		INSERT OR REPLACE INTO search_index(rowid, session_id, kind, content, ts)
		VALUES (?, ?, ?, ?, ?)
	`, rowID, sessionID, kind, content, ts)
	return err
}

func searchIndexRowID(parts ...string) int64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	id := int64(h.Sum64() & ((1 << 63) - 1))
	if id == 0 {
		return 1
	}
	return id
}

func (s *DB) backfillSearchIndexIfNeeded() error {
	if s.searchFallbackLike {
		return nil
	}

	var sourceRows int
	if err := s.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM tool_calls WHERE COALESCE(params_summary, '') != '' OR COALESCE(result_summary, '') != '') +
			(SELECT COUNT(*) FROM file_changes WHERE COALESCE(file_path, '') != '')
	`).Scan(&sourceRows); err != nil {
		return err
	}
	if sourceRows == 0 {
		return setSchemaVersion(s.db, searchIndexBackfillKey, searchIndexBackfillVersion)
	}

	var indexRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM search_index`).Scan(&indexRows); err != nil {
		return err
	}

	version := 0
	err := s.db.QueryRow(`SELECT version FROM schema_version WHERE key = ?`, searchIndexBackfillKey).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if version >= searchIndexBackfillVersion && indexRows > 0 {
		return nil
	}

	if _, err := s.db.Exec(`DELETE FROM search_index`); err != nil {
		return err
	}
	if err := s.backfillSearchIndexRows(); err != nil {
		return err
	}
	return setSchemaVersion(s.db, searchIndexBackfillKey, searchIndexBackfillVersion)
}

func setSchemaVersion(exec dailyCostCacheExecer, key string, version int) error {
	_, err := exec.Exec(`
		INSERT INTO schema_version(key, version)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET version = excluded.version
	`, key, version)
	return err
}

func (s *DB) backfillSearchIndexRows() error {
	toolRows, err := s.loadToolRowsForSearchBackfill()
	if err != nil {
		return err
	}
	for _, row := range toolRows {
		if strings.TrimSpace(row.params) != "" {
			if err := insertSearchIndexRow(s.db, searchIndexRowID("tool_param", row.callID), row.sessionID, "tool_param", row.params, row.startTime); err != nil {
				return err
			}
		}
		if strings.TrimSpace(row.result) != "" {
			if err := insertSearchIndexRow(s.db, searchIndexRowID("tool_result", row.callID), row.sessionID, "tool_result", row.result, row.endTime); err != nil {
				return err
			}
		}
	}

	fileRows, err := s.loadFileRowsForSearchBackfill()
	if err != nil {
		return err
	}
	for _, row := range fileRows {
		if err := insertSearchIndexRow(s.db, searchIndexRowID("file", row.sessionID, row.indexID), row.sessionID, "file", row.path, row.timestamp); err != nil {
			return err
		}
	}
	return nil
}

func (s *DB) loadToolRowsForSearchBackfill() ([]struct {
	callID    string
	sessionID string
	params    string
	result    string
	startTime string
	endTime   string
}, error) {
	rows, err := s.db.Query(`
		SELECT call_id, session_id, COALESCE(params_summary, ''), COALESCE(result_summary, ''),
		       start_time, COALESCE(NULLIF(end_time, ''), start_time)
		FROM tool_calls
		WHERE COALESCE(params_summary, '') != '' OR COALESCE(result_summary, '') != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []struct {
		callID    string
		sessionID string
		params    string
		result    string
		startTime string
		endTime   string
	}
	for rows.Next() {
		var row struct {
			callID    string
			sessionID string
			params    string
			result    string
			startTime string
			endTime   string
		}
		if err := rows.Scan(&row.callID, &row.sessionID, &row.params, &row.result, &row.startTime, &row.endTime); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *DB) loadFileRowsForSearchBackfill() ([]struct {
	sessionID string
	indexID   string
	path      string
	timestamp string
}, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, file_path, timestamp, COALESCE(source_id, '')
		FROM file_changes
		WHERE COALESCE(file_path, '') != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []struct {
		sessionID string
		indexID   string
		path      string
		timestamp string
	}
	for rows.Next() {
		var (
			id       int64
			sourceID string
			row      struct {
				sessionID string
				indexID   string
				path      string
				timestamp string
			}
		)
		if err := rows.Scan(&id, &row.sessionID, &row.path, &row.timestamp, &sourceID); err != nil {
			return nil, err
		}
		row.indexID = sourceID
		if row.indexID == "" {
			row.indexID = row.sessionID + ":" + row.path + ":" + row.timestamp
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func sanitizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if len(query) >= 2 && strings.HasPrefix(query, `"`) && strings.HasSuffix(query, `"`) {
		phrase := strings.TrimSpace(query[1 : len(query)-1])
		if phrase == "" {
			return ""
		}
		return `"` + strings.ReplaceAll(phrase, `"`, `""`) + `"`
	}

	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		prefix := strings.HasSuffix(field, "*")
		field = strings.TrimSuffix(field, "*")
		for _, token := range ftsTokens(field) {
			if prefix || token != "" {
				terms = append(terms, token+"*")
			}
		}
	}
	return strings.Join(terms, " ")
}

func ftsTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
