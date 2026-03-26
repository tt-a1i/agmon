package storage

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agmon", "data", "agmon.db")
}

func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite allows only one writer at a time. A single connection serializes
	// all access and eliminates SQLITE_BUSY when multiple goroutines write concurrently.
	db.SetMaxOpenConns(1)

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
	_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	// SQLite (including modernc.org/sqlite) returns "duplicate column name: <col>"
	// when the column already exists. We treat this as a no-op for idempotent migrations.
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		log.Printf("alter table %s add %s: %v", table, column, err)
	}
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
			timestamp   TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_agents_session ON agents(session_id);
		CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
		CREATE INDEX IF NOT EXISTS idx_tool_calls_agent ON tool_calls(agent_id);
		CREATE INDEX IF NOT EXISTS idx_token_usage_session ON token_usage(session_id);
		CREATE INDEX IF NOT EXISTS idx_file_changes_session ON file_changes(session_id);
	`)
	if err != nil {
		return err
	}
	// Schema migrations for new columns (idempotent).
	s.addColumnIfMissing("sessions", "last_event_time", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "cwd", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "git_branch", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "latest_context_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("sessions", "model", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "total_cache_read_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("sessions", "total_cache_creation_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("token_usage", "source_id", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("token_usage", "cache_creation_tokens", "INT NOT NULL DEFAULT 0")
	s.addColumnIfMissing("token_usage", "cache_read_tokens", "INT NOT NULL DEFAULT 0")
	_, err = s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_token_usage_source
		ON token_usage(source_id) WHERE source_id != ''
	`)
	return err
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

func (s *DB) UpsertSession(sessionID string, platform event.Platform, startTime time.Time) error {
	ts := startTime.UTC().Format(time.RFC3339)
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
	endStr := endTime.UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = 'ended',
			end_time = ?,
			last_event_time = CASE
				WHEN last_event_time = '' OR last_event_time < ? THEN ?
				ELSE last_event_time
			END
		WHERE session_id = ?
	`, endStr, endStr, endStr, sessionID)
	return err
}

func (s *DB) UpsertAgent(agentID, sessionID, parentAgentID, role string, startTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO agents (agent_id, session_id, parent_agent_id, role, start_time)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, sessionID, parentAgentID, role, startTime.UTC().Format(time.RFC3339))
	return err
}

func (s *DB) EndAgent(agentID string, endTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE agents SET status = 'ended', end_time = ? WHERE agent_id = ?
	`, endTime.UTC().Format(time.RFC3339), agentID)
	return err
}

func (s *DB) InsertToolCallStart(callID, agentID, sessionID, toolName, params string, startTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO tool_calls (call_id, agent_id, session_id, tool_name, params_summary, start_time)
		VALUES (?, ?, ?, ?, ?, ?)
	`, callID, agentID, sessionID, toolName, params, startTime.UTC().Format(time.RFC3339))
	return err
}

func (s *DB) UpdateToolCallEnd(callID, result string, status event.ToolCallStatus, durationMs int64, endTime time.Time) error {
	endStr := endTime.UTC().Format(time.RFC3339)
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
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO token_usage
			(agent_id, session_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, model, cost_usd, timestamp, source_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, agentID, sessionID, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, model, costUSD, ts.UTC().Format(time.RFC3339), sourceID)
	return err
}

// CleanOldSessions deletes all non-active sessions (ended/stale) older than olderThanDays days,
// along with their associated agents, tool_calls, token_usage, and file_changes records.
// Returns the number of sessions deleted.
func (s *DB) CleanOldSessions(olderThanDays int) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)
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
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET status = 'stale', end_time = ?
		WHERE status = 'active'
		  AND COALESCE(NULLIF(last_event_time, ''), start_time) < ?
	`, now, cutoff)
	return err
}

// BackfillEmptyTokenModel updates token_usage rows with empty model for a session,
// setting the model and recalculating cost using the given per-million-token prices.
// Returns the number of rows affected.
func (s *DB) BackfillEmptyTokenModel(sessionID, model string, inputPricePerM, outputPricePerM float64) (int64, error) {
	result, err := s.db.Exec(`
		UPDATE token_usage SET
			model = ?,
			cost_usd = (CAST(input_tokens AS REAL) * ? + CAST(output_tokens AS REAL) * ?) / 1000000.0
		WHERE session_id = ? AND model = ''
	`, model, inputPricePerM, outputPricePerM, sessionID)
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
		       COALESCE((SELECT t2.model FROM token_usage t2 WHERE t2.session_id = sub.session_id AND t2.model != '' LIMIT 1), ''),
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
			latest_context_tokens = COALESCE((SELECT input_tokens FROM token_usage WHERE session_id = ? ORDER BY timestamp DESC LIMIT 1), 0),
			model = COALESCE(NULLIF((SELECT model FROM token_usage WHERE session_id = ? AND model != '' ORDER BY timestamp DESC LIMIT 1), ''), model)
		WHERE session_id = ?
	`, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID, sessionID)
	return err
}

func (s *DB) InsertFileChange(sessionID, filePath string, changeType event.FileChangeType, ts time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO file_changes (session_id, file_path, change_type, timestamp)
		VALUES (?, ?, ?, ?)
	`, sessionID, filePath, string(changeType), ts.UTC().Format(time.RFC3339))
	return err
}
