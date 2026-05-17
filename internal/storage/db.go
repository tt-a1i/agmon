package storage

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/event"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

const storageTimeLayout = "2006-01-02T15:04:05.000000000Z"

func formatStorageTime(t time.Time) string {
	return t.UTC().Format(storageTimeLayout)
}

func parseStorageTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t.UTC(), true
}

func normalizeStorageTime(s string) string {
	t, ok := parseStorageTime(s)
	if !ok {
		return s
	}
	return formatStorageTime(t)
}

func DefaultDBPath() string {
	return appdir.PathFor("tokenmeter.db", "agmon.db", "data")
}

func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite allows only one writer at a time. A single connection serializes
	// all access and eliminates SQLITE_BUSY when multiple goroutines write concurrently.
	db.SetMaxOpenConns(1)

	// Set pragmas explicitly — DSN parameters may not be recognized by modernc.org/sqlite.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	s := &DB{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) addColumnIfMissing(table, column, def string) {
	// Check column existence up-front via PRAGMA table_info, which is stable
	// across SQLite driver versions. The previous implementation parsed the
	// driver's error string ("duplicate column name: ..."), which would break
	// silently if modernc.org/sqlite ever changed the wording.
	exists, err := s.columnExists(table, column)
	if err != nil {
		log.Printf("check column %s.%s: %v", table, column, err)
		return
	}
	if exists {
		return
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def)); err != nil {
		log.Printf("alter table %s add %s: %v", table, column, err)
	}
}

func (s *DB) columnExists(table, column string) (bool, error) {
	// PRAGMA table_info is non-parameterizable on the table name in SQLite;
	// table names come from string literals in migrate(), not user input.
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *DB) migrate() error {
	_, err := s.db.Exec(`
			CREATE TABLE IF NOT EXISTS sessions (
				session_id TEXT PRIMARY KEY,
				platform   TEXT NOT NULL,
				start_time TEXT NOT NULL,
				last_event_time TEXT NOT NULL DEFAULT '',
				end_time   TEXT,
				status     TEXT NOT NULL DEFAULT 'active',
				total_input_tokens  INTEGER NOT NULL DEFAULT 0,
				total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cost_usd      REAL NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS agents (
			agent_id        TEXT PRIMARY KEY,
			session_id      TEXT NOT NULL REFERENCES sessions(session_id),
			parent_agent_id TEXT,
			role            TEXT,
			status          TEXT NOT NULL DEFAULT 'active',
			start_time      TEXT NOT NULL,
			end_time        TEXT
		);

		CREATE TABLE IF NOT EXISTS tool_calls (
			call_id        TEXT PRIMARY KEY,
			agent_id       TEXT NOT NULL,
			session_id     TEXT NOT NULL REFERENCES sessions(session_id),
			tool_name      TEXT NOT NULL,
			params_summary TEXT,
			result_summary TEXT,
			start_time     TEXT NOT NULL,
			end_time       TEXT,
			duration_ms    INTEGER,
			status         TEXT NOT NULL DEFAULT 'pending'
		);

		CREATE TABLE IF NOT EXISTS token_usage (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id      TEXT NOT NULL,
			session_id    TEXT NOT NULL REFERENCES sessions(session_id),
			input_tokens  INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			model         TEXT,
			cost_usd      REAL NOT NULL DEFAULT 0,
			timestamp     TEXT NOT NULL
		);

			CREATE TABLE IF NOT EXISTS file_changes (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id  TEXT NOT NULL REFERENCES sessions(session_id),
				file_path   TEXT NOT NULL,
				change_type TEXT NOT NULL,
				timestamp   TEXT NOT NULL,
				source_id   TEXT NOT NULL DEFAULT ''
			);

			CREATE TABLE IF NOT EXISTS budgets (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				name        TEXT NOT NULL,
				monthly_usd REAL NOT NULL,
				platform    TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				updated_at  TEXT NOT NULL
			);

		CREATE INDEX IF NOT EXISTS idx_agents_session ON agents(session_id);
		CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
		CREATE INDEX IF NOT EXISTS idx_tool_calls_agent ON tool_calls(agent_id);
		CREATE INDEX IF NOT EXISTS idx_tool_calls_start ON tool_calls(start_time);
		CREATE INDEX IF NOT EXISTS idx_token_usage_session ON token_usage(session_id);
		CREATE INDEX IF NOT EXISTS idx_token_usage_ts ON token_usage(timestamp);
		CREATE INDEX IF NOT EXISTS idx_file_changes_session ON file_changes(session_id);
		CREATE INDEX IF NOT EXISTS idx_file_changes_ts ON file_changes(timestamp);
		CREATE INDEX IF NOT EXISTS idx_budgets_platform ON budgets(platform);
	`)
	if err != nil {
		return err
	}
	// Schema migrations for new columns (idempotent).
	s.addColumnIfMissing("sessions", "last_event_time", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "cwd", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "git_branch", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "latest_context_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("sessions", "latest_token_time", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "model", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "total_cache_read_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("sessions", "total_cache_creation_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("sessions", "tag", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("token_usage", "source_id", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("token_usage", "cache_creation_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("token_usage", "cache_read_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("file_changes", "source_id", "TEXT NOT NULL DEFAULT ''")

	// Schema version controls one-shot migrations (time normalization, stale
	// session fixup) so they don't re-scan all rows on every daemon restart.
	// Bump schemaVersion when you add a new one-shot step.
	const schemaVersion = 1
	var userVersion int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if userVersion < schemaVersion {
		if err := s.normalizeTimeColumns(); err != nil {
			return err
		}
		if _, err := s.db.Exec(`
			UPDATE sessions
			SET end_time = COALESCE(NULLIF(last_event_time, ''), start_time)
			WHERE status = 'stale'
			  AND COALESCE(NULLIF(last_event_time, ''), start_time) != ''
			  AND (end_time IS NULL OR end_time = '' OR end_time > COALESCE(NULLIF(last_event_time, ''), start_time))
		`); err != nil {
			return err
		}
		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	}
	_, err = s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_token_usage_source
		ON token_usage(source_id) WHERE source_id != ''
	`)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(`DROP INDEX IF EXISTS idx_file_changes_source`); err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_file_changes_source
		ON file_changes(session_id, source_id) WHERE source_id != ''
	`)
	return err
}

func (s *DB) normalizeTimeColumns() error {
	specs := []struct {
		table   string
		columns []string
	}{
		{table: "sessions", columns: []string{"start_time", "last_event_time", "end_time"}},
		{table: "agents", columns: []string{"start_time", "end_time"}},
		{table: "tool_calls", columns: []string{"start_time", "end_time"}},
		{table: "token_usage", columns: []string{"timestamp"}},
		{table: "file_changes", columns: []string{"timestamp"}},
	}

	for _, spec := range specs {
		query := fmt.Sprintf("SELECT rowid, %s FROM %s", strings.Join(spec.columns, ", "), spec.table)
		rows, err := s.db.Query(query)
		if err != nil {
			return err
		}
		type pendingUpdate struct {
			query string
			args  []any
		}
		var updates []pendingUpdate

		for rows.Next() {
			var rowID int64
			values := make([]sql.NullString, len(spec.columns))
			dest := make([]any, 0, len(spec.columns)+1)
			dest = append(dest, &rowID)
			for i := range values {
				dest = append(dest, &values[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}

			var sets []string
			var args []any
			for i, value := range values {
				if !value.Valid || value.String == "" {
					continue
				}
				normalized := normalizeStorageTime(value.String)
				if normalized != value.String {
					sets = append(sets, fmt.Sprintf("%s = ?", spec.columns[i]))
					args = append(args, normalized)
				}
			}
			if len(sets) == 0 {
				continue
			}
			args = append(args, rowID)
			update := fmt.Sprintf("UPDATE %s SET %s WHERE rowid = ?", spec.table, strings.Join(sets, ", "))
			updates = append(updates, pendingUpdate{query: update, args: args})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, update := range updates {
			if _, err := s.db.Exec(update.query, update.args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateSessionMeta sets cwd and git_branch on a session.
// Use this for authoritative metadata, such as SessionStart hook events.
func (s *DB) UpdateSessionMeta(sessionID, cwd, gitBranch string) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET
			cwd        = CASE WHEN ? != '' THEN ? ELSE cwd END,
			git_branch = CASE WHEN ? != '' THEN ? ELSE git_branch END
		WHERE session_id = ?
	`, cwd, cwd, gitBranch, gitBranch, sessionID)
	return err
}

// FillSessionMeta fills missing cwd and git_branch values without overwriting.
// Use this for best-effort metadata inferred from transcript/log events.
func (s *DB) FillSessionMeta(sessionID, cwd, gitBranch string) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET
			cwd        = CASE WHEN cwd = '' AND ? != '' THEN ? ELSE cwd END,
			git_branch = CASE WHEN git_branch = '' AND ? != '' THEN ? ELSE git_branch END
		WHERE session_id = ?
	`, cwd, cwd, gitBranch, gitBranch, sessionID)
	return err
}

// MarkPendingToolCallsInterrupted marks all pending tool calls for a session as interrupted.
func (s *DB) MarkPendingToolCallsInterrupted(sessionID string) error {
	_, err := s.db.Exec(`
		UPDATE tool_calls SET status = 'interrupted'
		WHERE session_id = ? AND status = 'pending'
	`, sessionID)
	return err
}

func (s *DB) CanEndSession(sessionID string, endTime time.Time) (bool, error) {
	var lastComparable string
	err := s.db.QueryRow(`
		SELECT COALESCE(NULLIF(last_event_time, ''), start_time)
		FROM sessions WHERE session_id = ?
	`, sessionID).Scan(&lastComparable)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return lastComparable <= formatStorageTime(endTime), nil
}

func (s *DB) UpsertSession(sessionID string, platform event.Platform, startTime time.Time) error {
	ts := formatStorageTime(startTime)
	_, err := s.db.Exec(`
		INSERT INTO sessions (session_id, platform, start_time, last_event_time, status)
		VALUES (?, ?, ?, ?, 'active')
		ON CONFLICT(session_id) DO UPDATE SET
			platform = excluded.platform,
			start_time = CASE
				WHEN sessions.start_time > excluded.start_time THEN excluded.start_time
				ELSE sessions.start_time
			END,
			last_event_time = CASE
				WHEN sessions.last_event_time = '' OR sessions.last_event_time < excluded.last_event_time THEN excluded.last_event_time
				ELSE sessions.last_event_time
			END,
			status = CASE
				WHEN sessions.end_time IS NULL OR sessions.end_time = '' OR excluded.last_event_time >= sessions.end_time THEN 'active'
				ELSE sessions.status
			END,
			end_time = CASE
				WHEN sessions.end_time IS NULL OR sessions.end_time = '' OR excluded.last_event_time >= sessions.end_time THEN NULL
				ELSE sessions.end_time
			END
	`, sessionID, string(platform), ts, ts)
	return err
}

func (s *DB) EndSession(sessionID string, endTime time.Time) error {
	endStr := formatStorageTime(endTime)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = CASE
				WHEN COALESCE(NULLIF(last_event_time, ''), start_time) <= ? THEN 'ended'
				ELSE status
			END,
			end_time = CASE
					WHEN COALESCE(NULLIF(last_event_time, ''), start_time) > ? THEN end_time
					WHEN end_time IS NULL OR end_time = '' OR end_time < ? THEN ?
					ELSE end_time
				END,
				last_event_time = CASE
					WHEN last_event_time = '' OR last_event_time < ? THEN ?
					ELSE last_event_time
				END
			WHERE session_id = ?
		`, endStr, endStr, endStr, endStr, endStr, endStr, sessionID)
	return err
}

func (s *DB) UpsertAgent(agentID, sessionID, parentAgentID, role string, startTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO agents (agent_id, session_id, parent_agent_id, role, start_time)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, sessionID, parentAgentID, role, formatStorageTime(startTime))
	return err
}

func (s *DB) EndAgent(agentID string, endTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE agents SET status = 'ended', end_time = ? WHERE agent_id = ?
	`, formatStorageTime(endTime), agentID)
	return err
}

// InsertToolCallStart records a tool call's "pre" event. Returns whether a
// new row was inserted — false means the call_id already existed (a Pre
// re-emit, which Claude shouldn't normally do). Callers can use this to
// measure how often re-emits occur before committing to an ON CONFLICT
// DO UPDATE policy.
func (s *DB) InsertToolCallStart(callID, agentID, sessionID, toolName, params string, startTime time.Time) (inserted bool, err error) {
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO tool_calls (call_id, agent_id, session_id, tool_name, params_summary, start_time)
		VALUES (?, ?, ?, ?, ?, ?)
	`, callID, agentID, sessionID, toolName, params, formatStorageTime(startTime))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *DB) UpdateToolCallEnd(callID, result string, status event.ToolCallStatus, durationMs int64, endTime time.Time) error {
	endStr := formatStorageTime(endTime)
	_, err := s.db.Exec(`
		UPDATE tool_calls SET result_summary = ?, status = ?,
			duration_ms = CASE WHEN ? > 0 THEN ?
				ELSE MAX(0, CAST(ROUND((julianday(?) - julianday(start_time)) * 86400000) AS INTEGER))
			END,
			end_time = ?
		WHERE call_id = ?
	`, result, string(status), durationMs, durationMs, endStr, endStr, callID)
	return err
}

// InsertTokenUsage inserts a token usage record.
// sourceID is used for deduplication (INSERT OR IGNORE on unique source_id).
// Pass a stable unique ID (e.g. message UUID) to prevent double-counting on daemon restart.
// Pass "" to skip dedup.
func (s *DB) InsertTokenUsage(agentID, sessionID string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int, model string, costUSD float64, ts time.Time, sourceID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tsStr := formatStorageTime(ts)
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO token_usage
			(agent_id, session_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, model, cost_usd, timestamp, source_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, agentID, sessionID, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, model, costUSD, tsStr, sourceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return tx.Commit()
	}

	_, err = tx.Exec(`
		UPDATE sessions SET
			total_input_tokens = total_input_tokens + ?,
			total_output_tokens = total_output_tokens + ?,
			total_cost_usd = total_cost_usd + ?,
			total_cache_read_tokens = total_cache_read_tokens + ?,
			total_cache_creation_tokens = total_cache_creation_tokens + ?,
			latest_context_tokens = CASE
				WHEN latest_token_time = '' OR latest_token_time <= ? THEN ?
				ELSE latest_context_tokens
			END,
			latest_token_time = CASE
				WHEN latest_token_time = '' OR latest_token_time <= ? THEN ?
				ELSE latest_token_time
			END,
			model = CASE
				WHEN ? != '' AND ? != '<synthetic>' AND (latest_token_time = '' OR latest_token_time <= ?) THEN ?
				ELSE model
			END
		WHERE session_id = ?
	`, inputTokens, outputTokens, costUSD, cacheReadTokens, cacheCreationTokens, tsStr, inputTokens, tsStr, tsStr, model, model, tsStr, model, sessionID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CleanOldSessions deletes all non-active sessions (ended/stale) older than olderThanDays days,
// along with their associated agents, tool_calls, token_usage, and file_changes records.
// Returns the number of sessions deleted.
func (s *DB) CleanOldSessions(olderThanDays int) (int, error) {
	cutoff := formatStorageTime(time.Now().UTC().AddDate(0, 0, -olderThanDays))
	whereClause := `status != 'active' AND COALESCE(end_time, NULLIF(last_event_time, ''), start_time) < ?`

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete related records first (SQLite FK not enforced by default).
	for _, q := range []string{
		`DELETE FROM file_changes WHERE session_id IN (SELECT session_id FROM sessions WHERE ` + whereClause + `)`,
		`DELETE FROM token_usage  WHERE session_id IN (SELECT session_id FROM sessions WHERE ` + whereClause + `)`,
		`DELETE FROM tool_calls   WHERE session_id IN (SELECT session_id FROM sessions WHERE ` + whereClause + `)`,
		`DELETE FROM agents       WHERE session_id IN (SELECT session_id FROM sessions WHERE ` + whereClause + `)`,
	} {
		if _, err := tx.Exec(q, cutoff); err != nil {
			return 0, err
		}
	}

	result, err := tx.Exec(`DELETE FROM sessions WHERE `+whereClause, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return int(n), nil
}

// MarkStaleSessionsEnded marks sessions that have been active longer than maxAge as stale.
// Called at daemon startup to clean up sessions that never received a SessionEnd event.
func (s *DB) MarkStaleSessionsEnded(maxAge time.Duration) error {
	cutoff := formatStorageTime(time.Now().UTC().Add(-maxAge))
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = 'stale',
			end_time = COALESCE(NULLIF(last_event_time, ''), start_time)
		WHERE status = 'active'
		  AND COALESCE(NULLIF(last_event_time, ''), start_time) < ?
	`, cutoff)
	return err
}

// PruneEmptyCodexSessions deletes Codex sessions that have zero tokens and zero
// tool calls. These are phantom sessions created by idle Codex background processes.
func (s *DB) PruneEmptyCodexSessions() (int64, error) {
	result, err := s.db.Exec(`
		DELETE FROM sessions WHERE platform = 'codex'
		AND total_input_tokens = 0 AND total_output_tokens = 0
		AND session_id NOT IN (SELECT DISTINCT session_id FROM tool_calls)
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RepairSyntheticModels fixes sessions whose model field is set to "<synthetic>"
// by looking up the most recent real model from their token_usage rows.
func (s *DB) RepairSyntheticModels() (int64, error) {
	result, err := s.db.Exec(`
		UPDATE sessions SET model = COALESCE((
			SELECT model FROM token_usage
			WHERE session_id = sessions.session_id
			  AND model != '' AND model != '<synthetic>'
			ORDER BY timestamp DESC LIMIT 1
		), '') WHERE model = '<synthetic>'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// BackfillEmptyTokenModel updates token_usage rows with empty model for a session,
// setting the model and recalculating cost using the given per-million-token prices.
// Returns the number of rows affected.
func (s *DB) BackfillEmptyTokenModel(sessionID, model string, inputPricePerM, outputPricePerM, cacheReadPricePerM float64) (int64, error) {
	result, err := s.db.Exec(`
		UPDATE token_usage SET
			model = ?,
			cost_usd = (
				CAST(MAX(input_tokens - cache_read_tokens, 0) AS REAL) * ? +
				CAST(cache_read_tokens AS REAL) * ? +
				CAST(output_tokens AS REAL) * ?
			) / 1000000.0
		WHERE session_id = ? AND model = ''
	`, model, inputPricePerM, cacheReadPricePerM, outputPricePerM, sessionID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// BackfillRecentCodexTokenModel updates the latest Codex token row at or just
// before a turn_context timestamp. It handles the edge case where a token_count
// line is observed before the turn_context line that carries the new model.
func (s *DB) BackfillRecentCodexTokenModel(sessionID, model string, contextTime time.Time, maxSkew time.Duration, inputPricePerM, outputPricePerM, cacheReadPricePerM float64) (int64, error) {
	if model == "" || maxSkew <= 0 {
		return 0, nil
	}
	contextStr := formatStorageTime(contextTime)
	minStr := formatStorageTime(contextTime.UTC().Add(-maxSkew))
	result, err := s.db.Exec(`
		UPDATE token_usage SET
			model = ?,
			cost_usd = (
				CAST(MAX(input_tokens - cache_read_tokens, 0) AS REAL) * ? +
				CAST(cache_read_tokens AS REAL) * ? +
				CAST(output_tokens AS REAL) * ?
			) / 1000000.0
		WHERE id = (
			SELECT id FROM token_usage
			WHERE session_id = ?
			  AND source_id LIKE 'codex-tokens-%'
			  AND timestamp >= ?
			  AND timestamp <= ?
			  AND model != ?
			  AND (model = '' OR cost_usd = 0)
			ORDER BY timestamp DESC, id DESC
			LIMIT 1
		)
	`, model, inputPricePerM, cacheReadPricePerM, outputPricePerM, sessionID, minStr, contextStr, model)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListEmptyModelSessions returns sessions that have token_usage rows with empty model,
// along with a known model from other rows in the same session (if any).
func (s *DB) ListEmptyModelSessions() ([]struct{ SessionID, Model, Platform string }, error) {
	rows, err := s.db.Query(`
		SELECT sub.session_id,
		       COALESCE((
		           SELECT CASE WHEN COUNT(DISTINCT t2.model) = 1 THEN MIN(t2.model) ELSE '' END
		           FROM token_usage t2
		           WHERE t2.session_id = sub.session_id
		             AND t2.model != '' AND t2.model != '<synthetic>'
		       ), ''),
		       s.platform
		FROM (SELECT DISTINCT session_id FROM token_usage WHERE model = '') sub
		JOIN sessions s ON sub.session_id = s.session_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []struct{ SessionID, Model, Platform string }
	for rows.Next() {
		var r struct{ SessionID, Model, Platform string }
		if err := rows.Scan(&r.SessionID, &r.Model, &r.Platform); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) UpdateSessionTokens(sessionID string) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET
			total_input_tokens = COALESCE((SELECT SUM(input_tokens) FROM token_usage WHERE session_id = ?), 0),
			total_output_tokens = COALESCE((SELECT SUM(output_tokens) FROM token_usage WHERE session_id = ?), 0),
			total_cost_usd = COALESCE((SELECT SUM(cost_usd) FROM token_usage WHERE session_id = ?), 0),
			total_cache_read_tokens = COALESCE((SELECT SUM(cache_read_tokens) FROM token_usage WHERE session_id = ?), 0),
			total_cache_creation_tokens = COALESCE((SELECT SUM(cache_creation_tokens) FROM token_usage WHERE session_id = ?), 0),
			latest_token_time = COALESCE((SELECT timestamp FROM token_usage WHERE session_id = ? ORDER BY timestamp DESC LIMIT 1), ''),
			latest_context_tokens = COALESCE((SELECT input_tokens FROM token_usage WHERE session_id = ? ORDER BY timestamp DESC LIMIT 1), 0),
			model = COALESCE(NULLIF((SELECT model FROM token_usage WHERE session_id = ? AND model != '' AND model != '<synthetic>' ORDER BY timestamp DESC LIMIT 1), ''), model)
		WHERE session_id = ?
	`, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID)
	return err
}

func (s *DB) InsertFileChange(sessionID, filePath string, changeType event.FileChangeType, ts time.Time) error {
	return s.InsertFileChangeWithSource(sessionID, filePath, changeType, ts, "")
}

func (s *DB) InsertFileChangeWithSource(sessionID, filePath string, changeType event.FileChangeType, ts time.Time, sourceID string) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO file_changes (session_id, file_path, change_type, timestamp, source_id)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, filePath, string(changeType), formatStorageTime(ts), sourceID)
	return err
}
