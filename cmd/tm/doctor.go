package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

const (
	doctorStatusOK      = "ok"
	doctorStatusWarning = "warning"
	doctorStatusError   = "error"
)

var (
	doctorDBWarnSizeBytes  int64 = 1 << 30
	doctorDBErrorSizeBytes int64 = 5 << 30
)

type doctorCheckResult struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	Fixed        bool   `json:"fixed,omitempty"`
	FixAttempted bool   `json:"fix_attempted,omitempty"`
	FixError     string `json:"fix_error,omitempty"`
	FixMessage   string `json:"fix_message,omitempty"`
	fix          func() error
}

type doctorFixReport struct {
	Checks             []doctorCheckResult `json:"checks"`
	FixedCount         int                 `json:"fixed_count"`
	ManualActionNeeded int                 `json:"manual_action_needed"`
	Actions            []doctorFixAction   `json:"actions"`
}

type doctorFixAction struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

type doctorContext struct {
	home       string
	appBase    string
	dbPath     string
	socketPath string
	pidPath    string
}

type doctorCheck struct {
	name string
	run  func(doctorContext) doctorCheckResult
}

func runDoctor() error {
	if maybePrintCmdHelp("doctor", os.Args[2:]) {
		return nil
	}
	jsonOutput := false
	fixMode := false
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--fix":
			fixMode = true
		default:
			return fmt.Errorf("unknown doctor argument: %s", arg)
		}
	}

	results := collectDoctorChecks()
	var report doctorFixReport
	if fixMode {
		results, report = applyDoctorFixes(results)
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if fixMode {
			return enc.Encode(report)
		}
		return enc.Encode(stripDoctorFixFuncs(results))
	}

	if fixMode {
		fmt.Println("TokenMeter Doctor (--fix mode)")
	} else {
		fmt.Println("TokenMeter Doctor — diagnostics")
	}
	fmt.Println()
	for _, result := range results {
		fmt.Printf("[%s] %s\n", doctorDisplayIcon(result), result.Message)
	}
	ok, warnings, errs := doctorCounts(results)
	fmt.Println()
	if fixMode {
		fmt.Printf("Summary: %d OK, %d warnings, %d still errors.\n", ok, warnings, errs)
		fmt.Printf("Fixed: %d issues. Manual action needed: %d.\n", report.FixedCount, report.ManualActionNeeded)
	} else {
		fmt.Printf("Summary: %d OK, %d warnings, %d errors.\n", ok, warnings, errs)
	}
	if doctorHooksNeedSetup(results) {
		fmt.Println("Run 'tm setup' if hooks missing.")
	}
	return nil
}

func collectDoctorChecks() []doctorCheckResult {
	home, _ := os.UserHomeDir()
	ctx := doctorContext{
		home:       home,
		appBase:    appdir.Base(),
		dbPath:     storage.DefaultDBPath(),
		socketPath: daemon.DefaultSocketPath(),
		pidPath:    appdir.Path("daemon.pid"),
	}

	checks := []doctorCheck{
		{"binary", checkDoctorBinary},
		{"home", checkDoctorHome},
		{"database_open", checkDoctorDatabaseOpen},
		{"sessions_query", checkDoctorListSessions},
		{"wal_mode", checkDoctorWALMode},
		{"schema_version", checkDoctorSchemaVersion},
		{"database_size", checkDoctorDatabaseSize},
		{"search_index", checkDoctorSearchIndex},
		{"socket_file", checkDoctorSocketFile},
		{"daemon_pid", checkDoctorDaemonPID},
		{"socket_dial", checkDoctorSocketDial},
		{"subscriber_socket", checkDoctorSubscriberSocket},
		{"event_stream", checkDoctorEventStream},
		{"claude_settings", checkDoctorClaudeSettingsJSON},
		{"claude_hook_command", checkDoctorClaudeHookCommand},
		{"claude_hook_events", checkDoctorClaudeHookEvents},
		{"codex_sessions", checkDoctorCodexSessions},
		{"pricing_json", checkDoctorPricingJSON},
		{"webhooks_json", checkDoctorWebhooksJSON},
		{"budgets", checkDoctorBudgets},
		{"backups_dir", checkDoctorBackupsDir},
		{"last_token_activity", checkDoctorLastTokenActivity},
		{"active_sessions", checkDoctorActiveSessions},
	}

	results := make([]doctorCheckResult, 0, len(checks))
	for _, check := range checks {
		result := safeDoctorCheck(check, ctx)
		results = append(results, result)
	}
	return results
}

func safeDoctorCheck(check doctorCheck, ctx doctorContext) (result doctorCheckResult) {
	defer func() {
		if r := recover(); r != nil {
			result = doctorResult(check.name, doctorStatusError, fmt.Sprintf("%s check failed: %v", check.name, r))
		}
	}()
	return check.run(ctx)
}

func doctorResult(name, status, message string) doctorCheckResult {
	return doctorCheckResult{Name: name, Status: status, Message: message}
}

func doctorFixableResult(name, status, message string, fix func() error) doctorCheckResult {
	return doctorCheckResult{Name: name, Status: status, Message: message, fix: fix}
}

func stripDoctorFixFuncs(results []doctorCheckResult) []doctorCheckResult {
	out := make([]doctorCheckResult, len(results))
	copy(out, results)
	for i := range out {
		out[i].fix = nil
	}
	return out
}

func applyDoctorFixes(results []doctorCheckResult) ([]doctorCheckResult, doctorFixReport) {
	fixed := make([]doctorCheckResult, len(results))
	copy(fixed, results)
	report := doctorFixReport{Checks: fixed}
	for i := range fixed {
		result := &fixed[i]
		if result.Status != doctorStatusError || result.fix == nil {
			continue
		}
		result.FixAttempted = true
		action := doctorFixAction{Name: result.Name}
		if err := result.fix(); err != nil {
			result.FixError = err.Error()
			action.Status = doctorStatusError
			action.Message = result.Message
			action.Error = err.Error()
			report.Actions = append(report.Actions, action)
			continue
		}
		result.Fixed = true
		result.Status = doctorStatusOK
		result.Message = result.Message + " → " + doctorFixedMessage(result.Name)
		result.FixMessage = doctorFixedMessage(result.Name)
		action.Status = doctorStatusOK
		action.Message = result.Message
		report.FixedCount++
		report.Actions = append(report.Actions, action)
	}
	report.Checks = stripDoctorFixFuncs(fixed)
	_, _, report.ManualActionNeeded = doctorCounts(fixed)
	return fixed, report
}

func doctorFixedMessage(name string) string {
	switch name {
	case "home":
		return "created app directories"
	case "schema_version":
		return "migration triggered"
	case "socket_file":
		return "fixed to 0600"
	case "daemon_pid":
		return "removed"
	case "claude_settings":
		return "backed up and restored defaults"
	case "claude_hook_command", "claude_hook_events":
		return "hooks installed"
	case "codex_sessions":
		return "created sessions directory"
	case "pricing_json":
		return "backed up and removed"
	case "webhooks_json":
		return "backed up and removed"
	case "backups_dir":
		return "created backups directory"
	default:
		return "fixed"
	}
}

func checkDoctorBinary(ctx doctorContext) doctorCheckResult {
	path, err := os.Executable()
	if err != nil {
		return doctorResult("binary", doctorStatusWarning, fmt.Sprintf("Binary path unavailable: %v", err))
	}
	return doctorResult("binary", doctorStatusOK, fmt.Sprintf("Binary at %s, version %s", path, version))
}

func checkDoctorHome(ctx doctorContext) doctorCheckResult {
	info, err := os.Stat(ctx.appBase)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorFixableResult("home", doctorStatusError, fmt.Sprintf("Home directory %s missing", ctx.appBase), func() error {
				for _, path := range []string{ctx.appBase, filepath.Join(ctx.appBase, "data"), filepath.Join(ctx.appBase, "backups")} {
					if err := os.MkdirAll(path, 0o755); err != nil {
						return err
					}
				}
				return nil
			})
		}
		return doctorResult("home", doctorStatusError, fmt.Sprintf("Home directory %s inaccessible: %v", ctx.appBase, err))
	}
	if !info.IsDir() {
		return doctorResult("home", doctorStatusError, fmt.Sprintf("Home path %s is not a directory", ctx.appBase))
	}
	probe := filepath.Join(ctx.appBase, ".doctor-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return doctorResult("home", doctorStatusError, fmt.Sprintf("Home directory %s not writable: %v", ctx.appBase, err))
	}
	_ = os.Remove(probe)
	return doctorResult("home", doctorStatusOK, fmt.Sprintf("Home directory %s exists and is readable/writable", ctx.appBase))
}

func checkDoctorDatabaseOpen(ctx doctorContext) doctorCheckResult {
	if _, err := os.Stat(ctx.dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorResult("database_open", doctorStatusError, fmt.Sprintf("Database %s missing", ctx.dbPath))
		}
		return doctorResult("database_open", doctorStatusError, fmt.Sprintf("Database %s not readable: %v", ctx.dbPath, err))
	}
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("database_open", doctorStatusError, fmt.Sprintf("Database %s open failed: %v", ctx.dbPath, err))
	}
	defer db.Close()
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		return doctorResult("database_open", doctorStatusError, fmt.Sprintf("Database %s readable but session query failed: %v", ctx.dbPath, err))
	}
	return doctorResult("database_open", doctorStatusOK, fmt.Sprintf("Database %s readable (%d sessions)", ctx.dbPath, sessions))
}

func checkDoctorListSessions(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("sessions_query", doctorStatusError, fmt.Sprintf("ListSessions unavailable: %v", err))
	}
	defer db.Close()
	var sessions int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sessions
		WHERE status = 'active'
		   OR total_input_tokens > 0 OR total_output_tokens > 0
		   OR total_cache_read_tokens > 0 OR total_cache_creation_tokens > 0
	`).Scan(&sessions); err != nil {
		return doctorResult("sessions_query", doctorStatusError, fmt.Sprintf("ListSessions failed: %v", err))
	}
	return doctorResult("sessions_query", doctorStatusOK, fmt.Sprintf("ListSessions query OK (%d visible sessions)", sessions))
}

func checkDoctorWALMode(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("wal_mode", doctorStatusError, fmt.Sprintf("Database WAL check unavailable: %v", err))
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return doctorResult("wal_mode", doctorStatusError, fmt.Sprintf("Database WAL mode check failed: %v", err))
	}
	if strings.EqualFold(mode, "wal") {
		return doctorResult("wal_mode", doctorStatusOK, "Database WAL mode enabled")
	}
	return doctorResult("wal_mode", doctorStatusWarning, fmt.Sprintf("Database WAL mode is %s, expected wal", mode))
}

func checkDoctorSchemaVersion(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("schema_version", doctorStatusError, fmt.Sprintf("Schema version check unavailable: %v", err))
	}
	defer db.Close()
	if !doctorTableExists(db, "schema_version") {
		return doctorFixableResult("schema_version", doctorStatusError, "Schema version table missing", func() error {
			migrated, err := storage.Open(ctx.dbPath)
			if err != nil {
				return err
			}
			return migrated.Close()
		})
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return doctorResult("schema_version", doctorStatusError, fmt.Sprintf("Schema version read failed: %v", err))
	}
	return doctorResult("schema_version", doctorStatusOK, fmt.Sprintf("Schema version: %d", version))
}

func checkDoctorDatabaseSize(ctx doctorContext) doctorCheckResult {
	info, err := os.Stat(ctx.dbPath)
	if err != nil {
		return doctorResult("database_size", doctorStatusError, fmt.Sprintf("Database size unavailable: %v", err))
	}
	size := info.Size()
	switch {
	case size > doctorDBErrorSizeBytes:
		return doctorResult("database_size", doctorStatusError, fmt.Sprintf("Database size %s — run 'tm clean 30' soon", formatBytes(size)))
	case size > doctorDBWarnSizeBytes:
		return doctorResult("database_size", doctorStatusWarning, fmt.Sprintf("Database size %s — consider 'tm clean 30'", formatBytes(size)))
	default:
		return doctorResult("database_size", doctorStatusOK, fmt.Sprintf("Database size %s", formatBytes(size)))
	}
}

func checkDoctorSearchIndex(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("search_index", doctorStatusWarning, fmt.Sprintf("FTS5 search index unavailable; SearchHits will use LIKE fallback: %v", err))
	}
	defer db.Close()
	if !doctorTableExists(db, "search_index") {
		return doctorResult("search_index", doctorStatusWarning, "FTS5 search_index missing; SearchHits will use LIKE fallback")
	}
	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM search_index").Scan(&rows); err != nil {
		return doctorResult("search_index", doctorStatusWarning, fmt.Sprintf("FTS5 search_index unreadable; SearchHits will use LIKE fallback: %v", err))
	}
	if rows == 0 {
		return doctorResult("search_index", doctorStatusWarning, "FTS5 search_index empty; it will populate after searchable activity")
	}
	return doctorResult("search_index", doctorStatusOK, fmt.Sprintf("FTS5 search_index present (%d rows)", rows))
}

func checkDoctorSocketFile(ctx doctorContext) doctorCheckResult {
	info, err := os.Stat(ctx.socketPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorResult("socket_file", doctorStatusError, fmt.Sprintf("Socket %s missing; daemon not running", ctx.socketPath))
		}
		return doctorResult("socket_file", doctorStatusError, fmt.Sprintf("Socket %s inaccessible: %v", ctx.socketPath, err))
	}
	mode := info.Mode().Perm()
	if runtime.GOOS != "windows" && mode != 0o600 {
		running, _ := daemonPIDRunning(ctx.pidPath)
		if running {
			return doctorResult("socket_file", doctorStatusError, fmt.Sprintf("Socket %s mode %04o, expected 0600; daemon running, skipped", ctx.socketPath, mode))
		}
		return doctorFixableResult("socket_file", doctorStatusError, fmt.Sprintf("Socket %s mode %04o, expected 0600", ctx.socketPath, mode), func() error {
			return os.Chmod(ctx.socketPath, 0o600)
		})
	}
	return doctorResult("socket_file", doctorStatusOK, fmt.Sprintf("Socket %s mode %04o", ctx.socketPath, mode))
}

func checkDoctorDaemonPID(ctx doctorContext) doctorCheckResult {
	running, pid := daemonPIDRunning(ctx.pidPath)
	if !running {
		data, err := os.ReadFile(ctx.pidPath)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			return doctorResult("daemon_pid", doctorStatusError, fmt.Sprintf("Daemon pid file %s missing; daemon not running", ctx.pidPath))
		}
		if err == nil {
			stalePID := strings.TrimSpace(string(data))
			return doctorFixableResult("daemon_pid", doctorStatusError, fmt.Sprintf("Stale daemon.pid (pid %s dead)", stalePID), func() error {
				return os.Remove(ctx.pidPath)
			})
		}
		return doctorResult("daemon_pid", doctorStatusError, fmt.Sprintf("Daemon pid file %s unreadable: %v", ctx.pidPath, err))
	}
	return doctorResult("daemon_pid", doctorStatusOK, fmt.Sprintf("Daemon running (pid %d)", pid))
}

func checkDoctorSocketDial(ctx doctorContext) doctorCheckResult {
	conn, err := dialDoctorSocket(ctx.socketPath)
	if err != nil {
		return doctorResult("socket_dial", doctorStatusError, fmt.Sprintf("Daemon socket unreachable: %v", err))
	}
	_ = conn.Close()
	return doctorResult("socket_dial", doctorStatusOK, "Daemon socket reachable")
}

func checkDoctorSubscriberSocket(ctx doctorContext) doctorCheckResult {
	_, closeFn, err := daemon.SubscribeRemote(ctx.socketPath)
	if err != nil {
		return doctorResult("subscriber_socket", doctorStatusError, fmt.Sprintf("Subscriber socket unreachable: %v", err))
	}
	closeFn()
	return doctorResult("subscriber_socket", doctorStatusOK, "Subscriber socket reachable")
}

func checkDoctorEventStream(ctx doctorContext) doctorCheckResult {
	_, closeFn, err := daemon.SubscribeRemote(ctx.socketPath)
	if err != nil {
		return doctorResult("event_stream", doctorStatusError, fmt.Sprintf("/api/events stream backend unavailable: %v", err))
	}
	closeFn()
	return doctorResult("event_stream", doctorStatusOK, "/api/events endpoint OK")
}

func checkDoctorClaudeSettingsJSON(ctx doctorContext) doctorCheckResult {
	path := claudeSettingsPath(ctx)
	if _, err := readClaudeSettings(path); err != nil {
		return doctorFixableResult("claude_settings", doctorStatusError, fmt.Sprintf("Claude settings %s invalid: %v", path, err), func() error {
			if err := backupFile(path, path+".bak"); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
				return err
			}
			return runDoctorSetupQuiet()
		})
	}
	return doctorResult("claude_settings", doctorStatusOK, fmt.Sprintf("Claude settings %s valid JSON", path))
}

func checkDoctorClaudeHookCommand(ctx doctorContext) doctorCheckResult {
	path := claudeSettingsPath(ctx)
	settings, err := readClaudeSettings(path)
	if err != nil {
		return doctorResult("claude_hook_command", doctorStatusError, fmt.Sprintf("Claude hooks unavailable: %v", err))
	}
	events := tokenMeterHookEvents(settings)
	if len(events) == 0 {
		return doctorFixableResult("claude_hook_command", doctorStatusError, "Claude hooks missing tm emit command", runDoctorSetupQuiet)
	}
	return doctorResult("claude_hook_command", doctorStatusOK, fmt.Sprintf("Claude hooks configured (%d entries: %s)", len(events), strings.Join(events, ", ")))
}

func checkDoctorClaudeHookEvents(ctx doctorContext) doctorCheckResult {
	path := claudeSettingsPath(ctx)
	settings, err := readClaudeSettings(path)
	if err != nil {
		return doctorResult("claude_hook_events", doctorStatusError, fmt.Sprintf("Claude hook events unavailable: %v", err))
	}
	events := tokenMeterHookEventSet(settings)
	var missing []string
	for _, hookName := range tokenmeterHookNames {
		if !events[hookName] {
			missing = append(missing, hookName)
		}
	}
	if len(missing) > 0 {
		return doctorFixableResult("claude_hook_events", doctorStatusError, fmt.Sprintf("Claude hooks missing events: %s", strings.Join(missing, ", ")), runDoctorSetupQuiet)
	}
	return doctorResult("claude_hook_events", doctorStatusOK, fmt.Sprintf("Claude hook events all registered (%d events)", len(tokenmeterHookNames)))
}

func checkDoctorCodexSessions(ctx doctorContext) doctorCheckResult {
	path := filepath.Join(ctx.home, ".codex", "sessions")
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorFixableResult("codex_sessions", doctorStatusError, fmt.Sprintf("Codex log dir %s missing — no codex sessions yet", path), func() error {
				return os.MkdirAll(path, 0o755)
			})
		}
		return doctorResult("codex_sessions", doctorStatusWarning, fmt.Sprintf("Codex log dir %s unreadable: %v", path, err))
	}
	if len(entries) == 0 {
		return doctorResult("codex_sessions", doctorStatusWarning, fmt.Sprintf("Codex log dir %s empty — no codex sessions yet", path))
	}
	return doctorResult("codex_sessions", doctorStatusOK, fmt.Sprintf("Codex log dir %s contains %d entries", path, len(entries)))
}

func checkDoctorPricingJSON(ctx doctorContext) doctorCheckResult {
	return checkOptionalJSONFile("pricing_json", appdir.PathFor("pricing.json", "pricing.json"), "Pricing override")
}

func checkDoctorWebhooksJSON(ctx doctorContext) doctorCheckResult {
	return checkOptionalJSONFile("webhooks_json", appdir.PathFor("webhooks.json", "webhooks.json"), "Webhooks")
}

func checkDoctorBudgets(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("budgets", doctorStatusError, fmt.Sprintf("Budgets table unavailable: %v", err))
	}
	defer db.Close()
	var budgets int
	if err := db.QueryRow("SELECT COUNT(*) FROM budgets").Scan(&budgets); err != nil {
		return doctorResult("budgets", doctorStatusError, fmt.Sprintf("Budgets table unreadable: %v", err))
	}
	return doctorResult("budgets", doctorStatusOK, fmt.Sprintf("Budgets table readable (%d budgets)", budgets))
}

func checkDoctorBackupsDir(ctx doctorContext) doctorCheckResult {
	path := filepath.Join(ctx.appBase, "backups")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorFixableResult("backups_dir", doctorStatusError, fmt.Sprintf("Backups dir %s missing", path), func() error {
				return os.MkdirAll(path, 0o755)
			})
		}
		return doctorResult("backups_dir", doctorStatusError, fmt.Sprintf("Backups dir %s inaccessible: %v", path, err))
	}
	if !info.IsDir() {
		return doctorResult("backups_dir", doctorStatusError, fmt.Sprintf("Backups path %s is not a directory", path))
	}
	return doctorResult("backups_dir", doctorStatusOK, fmt.Sprintf("Backups dir %s exists", path))
}

func checkDoctorLastTokenActivity(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("last_token_activity", doctorStatusWarning, fmt.Sprintf("Last token activity unavailable: %v", err))
	}
	defer db.Close()
	var ts sql.NullString
	if err := db.QueryRow("SELECT MAX(timestamp) FROM token_usage").Scan(&ts); err != nil {
		return doctorResult("last_token_activity", doctorStatusWarning, fmt.Sprintf("Last token activity query failed: %v", err))
	}
	if !ts.Valid || ts.String == "" {
		return doctorResult("last_token_activity", doctorStatusWarning, "Last token activity: none recorded")
	}
	last, err := time.Parse(time.RFC3339Nano, ts.String)
	if err != nil {
		last, err = time.Parse(time.RFC3339, ts.String)
	}
	if err != nil {
		return doctorResult("last_token_activity", doctorStatusWarning, fmt.Sprintf("Last token activity timestamp invalid: %s", ts.String))
	}
	age := time.Since(last)
	message := fmt.Sprintf("Last token activity: %s ago", formatAge(age))
	if age <= 24*time.Hour {
		return doctorResult("last_token_activity", doctorStatusOK, message)
	}
	if age <= 7*24*time.Hour {
		return doctorResult("last_token_activity", doctorStatusWarning, message+" — no activity in the last 24h")
	}
	return doctorResult("last_token_activity", doctorStatusWarning, message+" — no recent activity")
}

func checkDoctorActiveSessions(ctx doctorContext) doctorCheckResult {
	db, err := openDoctorSQL(ctx.dbPath)
	if err != nil {
		return doctorResult("active_sessions", doctorStatusError, fmt.Sprintf("Active sessions unavailable: %v", err))
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions WHERE status = 'active'").Scan(&count); err != nil {
		return doctorResult("active_sessions", doctorStatusError, fmt.Sprintf("Active sessions query failed: %v", err))
	}
	return doctorResult("active_sessions", doctorStatusOK, fmt.Sprintf("Active sessions: %d", count))
}

func openDoctorSQL(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func doctorTableExists(db *sql.DB, name string) bool {
	var got string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ?", name).Scan(&got)
	return err == nil && got == name
}

func dialDoctorSocket(path string) (net.Conn, error) {
	if runtime.GOOS == "windows" {
		addr, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return net.DialTimeout("tcp", strings.TrimSpace(string(addr)), time.Second)
	}
	return net.DialTimeout("unix", path, time.Second)
}

func claudeSettingsPath(ctx doctorContext) string {
	return filepath.Join(ctx.home, ".claude", "settings.json")
}

func readClaudeSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func tokenMeterHookEvents(settings map[string]any) []string {
	events := tokenMeterHookEventSet(settings)
	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func tokenMeterHookEventSet(settings map[string]any) map[string]bool {
	result := make(map[string]bool)
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return result
	}
	for eventName, rawEntries := range hooks {
		entries, ok := rawEntries.([]any)
		if !ok {
			continue
		}
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			inner, ok := entry["hooks"].([]any)
			if !ok {
				continue
			}
			for _, rawHook := range inner {
				hook, ok := rawHook.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := hook["command"].(string)
				if isTokenMeterEmitCommand(cmd) {
					result[eventName] = true
				}
			}
		}
	}
	return result
}

func checkOptionalJSONFile(name, path, label string) doctorCheckResult {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorResult(name, doctorStatusOK, fmt.Sprintf("%s %s absent (using defaults)", label, path))
		}
		return doctorResult(name, doctorStatusError, fmt.Sprintf("%s %s unreadable: %v", label, path, err))
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return doctorFixableResult(name, doctorStatusError, fmt.Sprintf("%s %s invalid JSON: %v", label, path, err), func() error {
			if err := backupFile(path, path+".bak"); err != nil {
				return err
			}
			return os.Remove(path)
		})
	}
	return doctorResult(name, doctorStatusOK, fmt.Sprintf("%s %s valid JSON", label, path))
}

func backupFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func runDoctorSetupQuiet() error {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	prev := os.Stdout
	os.Stdout = devNull
	defer func() { os.Stdout = prev }()
	runSetup()
	return nil
}

func doctorIcon(status string) string {
	switch status {
	case doctorStatusOK:
		return "✓"
	case doctorStatusWarning:
		return "⚠"
	default:
		return "✗"
	}
}

func doctorDisplayIcon(result doctorCheckResult) string {
	if result.Fixed {
		return "✗→✓"
	}
	return doctorIcon(result.Status)
}

func doctorCounts(results []doctorCheckResult) (ok, warnings, errs int) {
	for _, result := range results {
		switch result.Status {
		case doctorStatusOK:
			ok++
		case doctorStatusWarning:
			warnings++
		default:
			errs++
		}
	}
	return ok, warnings, errs
}

func doctorHooksNeedSetup(results []doctorCheckResult) bool {
	for _, result := range results {
		if result.Status == doctorStatusError && strings.Contains(result.Name, "claude_hook") {
			return true
		}
	}
	return false
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1fPB", value/unit)
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + " seconds"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " minutes"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " hours"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " days"
	}
}
