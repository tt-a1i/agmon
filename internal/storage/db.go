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
	s.addColumnIfMissing("sessions", "cwd", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("sessions", "git_branch", "TEXT NOT NULL DEFAULT ''")
	s.addColumnIfMissing("token_usage", "source_id", "TEXT NOT NULL DEFAULT ''")
	_, err = s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_token_usage_source
		ON token_usage(source_id) WHERE source_id != ''
	`)
	return err
}

// UpdateSessionMeta sets cwd and git_branch on a session.
// Only overwrites a field if the new value is non-empty.
func (s *DB) UpdateSessionMeta(sessionID, cwd, gitBranch string) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET
			cwd        = CASE WHEN ? != '' THEN ? ELSE cwd END,
			git_branch = CASE WHEN ? != '' THEN ? ELSE git_branch END
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
	_, err := s.db.Exec(`
		INSERT INTO sessions (session_id, platform, start_time)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO NOTHING
	`, sessionID, string(platform), startTime.Format(time.RFC3339))
	return err
}

func (s *DB) EndSession(sessionID string, endTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET status = 'ended', end_time = ? WHERE session_id = ?
	`, endTime.Format(time.RFC3339), sessionID)
	return err
}

func (s *DB) UpsertAgent(agentID, sessionID, parentAgentID, role string, startTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO agents (agent_id, session_id, parent_agent_id, role, start_time)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, sessionID, parentAgentID, role, startTime.Format(time.RFC3339))
	return err
}

func (s *DB) EndAgent(agentID string, endTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE agents SET status = 'ended', end_time = ? WHERE agent_id = ?
	`, endTime.Format(time.RFC3339), agentID)
	return err
}

func (s *DB) InsertToolCallStart(callID, agentID, sessionID, toolName, params string, startTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO tool_calls (call_id, agent_id, session_id, tool_name, params_summary, start_time)
		VALUES (?, ?, ?, ?, ?, ?)
	`, callID, agentID, sessionID, toolName, params, startTime.Format(time.RFC3339))
	return err
}

func (s *DB) UpdateToolCallEnd(callID, result string, status event.ToolCallStatus, durationMs int64, endTime time.Time) error {
	_, err := s.db.Exec(`
		UPDATE tool_calls SET result_summary = ?, status = ?, duration_ms = ?, end_time = ?
		WHERE call_id = ?
	`, result, string(status), durationMs, endTime.Format(time.RFC3339), callID)
	return err
}

// InsertTokenUsage inserts a token usage record.
// sourceID is used for deduplication (INSERT OR IGNORE on unique source_id).
// Pass a stable unique ID (e.g. message UUID) to prevent double-counting on daemon restart.
// Pass "" to skip dedup.
func (s *DB) InsertTokenUsage(agentID, sessionID string, inputTokens, outputTokens int, model string, costUSD float64, ts time.Time, sourceID string) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO token_usage
			(agent_id, session_id, input_tokens, output_tokens, model, cost_usd, timestamp, source_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, agentID, sessionID, inputTokens, outputTokens, model, costUSD, ts.Format(time.RFC3339), sourceID)
	return err
}

// CleanOldSessions deletes all non-active sessions (ended/stale) older than olderThanDays days,
// along with their associated agents, tool_calls, token_usage, and file_changes records.
// Returns the number of sessions deleted.
func (s *DB) CleanOldSessions(olderThanDays int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)

	// Delete related records first (SQLite FK not enforced by default).
	for _, q := range []string{
		`DELETE FROM file_changes WHERE session_id IN (SELECT session_id FROM sessions WHERE status != 'active' AND start_time < ?)`,
		`DELETE FROM token_usage  WHERE session_id IN (SELECT session_id FROM sessions WHERE status != 'active' AND start_time < ?)`,
		`DELETE FROM tool_calls   WHERE session_id IN (SELECT session_id FROM sessions WHERE status != 'active' AND start_time < ?)`,
		`DELETE FROM agents       WHERE session_id IN (SELECT session_id FROM sessions WHERE status != 'active' AND start_time < ?)`,
	} {
		if _, err := s.db.Exec(q, cutoff); err != nil {
			return 0, err
		}
	}

	result, err := s.db.Exec(`DELETE FROM sessions WHERE status != 'active' AND start_time < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// MarkStaleSessionsEnded marks sessions that have been active longer than maxAge as stale.
// Called at daemon startup to clean up sessions that never received a SessionEnd event.
func (s *DB) MarkStaleSessionsEnded(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE sessions SET status = 'stale', end_time = ?
		WHERE status = 'active' AND start_time < ?
	`, now, cutoff)
	return err
}

func (s *DB) UpdateSessionTokens(sessionID string) error {
	_, err := s.db.Exec(`
		UPDATE sessions SET
			total_input_tokens = COALESCE((SELECT SUM(input_tokens) FROM token_usage WHERE session_id = ?), 0),
			total_output_tokens = COALESCE((SELECT SUM(output_tokens) FROM token_usage WHERE session_id = ?), 0),
			total_cost_usd = COALESCE((SELECT SUM(cost_usd) FROM token_usage WHERE session_id = ?), 0)
		WHERE session_id = ?
	`, sessionID, sessionID, sessionID, sessionID)
	return err
}

func (s *DB) InsertFileChange(sessionID, filePath string, changeType event.FileChangeType, ts time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO file_changes (session_id, file_path, change_type, timestamp)
		VALUES (?, ?, ?, ?)
	`, sessionID, filePath, string(changeType), ts.Format(time.RFC3339))
	return err
}
