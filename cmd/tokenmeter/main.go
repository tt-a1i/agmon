package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/report"
	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/tui"
	"github.com/tt-a1i/tokenmeter/internal/web"
)

var version = "dev"

var tokenmeterHookNames = []string{
	"SessionStart", "SessionEnd", "Stop",
	"PreToolUse", "PostToolUse", "PostToolUseFailure",
	"SubagentStart", "SubagentStop",
}

type daemonMetricsProvider struct {
	daemon *daemon.Daemon
	db     *storage.DB
}

func (p daemonMetricsProvider) DaemonStats() (int64, int64, int64) {
	if p.daemon == nil {
		return 0, 0, 0
	}
	return p.daemon.Stats()
}

func (p daemonMetricsProvider) BudgetUsageAll() ([]web.BudgetMetric, error) {
	budgets, err := p.db.ListBudgets()
	if err != nil {
		return nil, err
	}
	result := make([]web.BudgetMetric, 0, len(budgets))
	for _, budget := range budgets {
		used, limit, err := p.db.GetBudgetUsage(budget.ID)
		if err != nil {
			return nil, err
		}
		percent := 0.0
		if limit > 0 {
			percent = used / limit * 100
		}
		result = append(result, web.BudgetMetric{
			Name:     budget.Name,
			Platform: budget.Platform,
			UsedUSD:  used,
			LimitUSD: limit,
			Percent:  percent,
		})
	}
	return result, nil
}

func defaultLogPath() string {
	return appdir.PathFor("tokenmeter.log", "agmon.log")
}

func configureTUILogging(logPath string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	prevWriter := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	log.SetOutput(f)

	return func() error {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		return f.Close()
	}, nil
}

func mustOpenDB() *storage.DB {
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	return db
}

type tuiOptions struct {
	workspace       string
	workspaceFilter bool
}

func isTUIFlag(arg string) bool {
	return arg == "--all" || arg == "--workspace" || strings.HasPrefix(arg, "--workspace=")
}

func parseTUIOptions(args []string, getwd func() (string, error)) (tuiOptions, error) {
	cwd, err := getwd()
	if err != nil {
		return tuiOptions{}, fmt.Errorf("get current workspace: %w", err)
	}
	workspace, err := cleanTUIWorkspace(cwd)
	if err != nil {
		return tuiOptions{}, err
	}
	opts := tuiOptions{
		workspace:       workspace,
		workspaceFilter: workspace != "",
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			opts.workspace = ""
			opts.workspaceFilter = false
		case arg == "--workspace":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--workspace requires a path")
			}
			i++
			workspace, err := cleanTUIWorkspace(args[i])
			if err != nil {
				return opts, err
			}
			opts.workspace = workspace
			opts.workspaceFilter = true
		case strings.HasPrefix(arg, "--workspace="):
			workspace, err := cleanTUIWorkspace(strings.TrimPrefix(arg, "--workspace="))
			if err != nil {
				return opts, err
			}
			opts.workspace = workspace
			opts.workspaceFilter = true
		default:
			return opts, fmt.Errorf("unknown TUI option: %s", arg)
		}
	}
	return opts, nil
}

func cleanTUIWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("workspace path cannot be empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func newTUIModel(db *storage.DB, tuiCh chan tui.EventMsg, opts tuiOptions) tui.Model {
	m := tui.NewModel(db, tuiCh)
	if opts.workspaceFilter && opts.workspace != "" {
		m = m.WithWorkspace(opts.workspace)
	}
	return m
}

func main() {
	if len(os.Args) < 2 || isTUIFlag(os.Args[1]) {
		runTUI(os.Args[1:]...)
		return
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "reload":
		if err := runReload(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "emit":
		runEmit()
	case "setup":
		runSetup()
	case "init":
		if err := runInit(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "uninstall":
		runUninstall()
	case "status":
		runStatus()
	case "report":
		runReport()
	case "share":
		runShare()
	case "cost":
		runCost()
	case "export":
		if err := runExport(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "compare":
		if err := runCompare(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "search":
		if err := runSearch(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "budget":
		if err := runBudget(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "webhook":
		if err := runWebhook(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "analyze":
		if err := runAnalyze(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "watch":
		if err := runWatch(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "top":
		if err := runTop(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "healthcheck":
		if err := runHealthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "logs":
		if err := runLogs(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "checkpoint":
		if err := runCheckpoint(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "completion":
		if err := runCompletion(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "backup":
		if err := runBackup(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "restore":
		if err := runRestore(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "doctor":
		if err := runDoctor(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "compact":
		if err := runCompact(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "clean":
		runClean()
	case "tag":
		runTag()
	case "web":
		if err := runWeb(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "update":
		runUpdate()
	case "version", "-v", "--version":
		if err := runVersion(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprint(os.Stderr, unknownCommandHelpMessage(os.Args[1]))
		printHelp()
		os.Exit(1)
	}
}

func latestOrRequestedSession(db *storage.DB, args []string) (storage.SessionRow, bool) {
	if len(args) > 2 {
		s, found, err := db.GetSessionByIDPrefix(args[2])
		if err != nil {
			log.Fatalf("lookup session: %v", err)
		}
		if !found {
			log.Fatalf("session not found: %s", args[2])
		}
		return s, true
	}

	sessions, err := db.ListSessions()
	if err != nil {
		log.Fatalf("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		return storage.SessionRow{}, false
	}
	return sessions[0], true
}

func runTUI(args ...string) {
	opts, err := parseTUIOptions(args, os.Getwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	db := mustOpenDB()
	defer db.Close()

	sockPath := daemon.DefaultSocketPath()

	// If daemon already running, connect TUI only but still subscribe for real-time events.
	if running, _ := daemon.IsRunning(); running {
		tuiCh := make(chan tui.EventMsg, 256)
		eventCh, closeFn, err := daemon.SubscribeRemote(sockPath)
		if err != nil {
			log.Printf("subscribe daemon events: %v", err)
		} else {
			defer closeFn()
			go func() {
				for range eventCh {
					select {
					case tuiCh <- tui.EventMsg{}:
					default:
					}
				}
			}()
		}

		m := newTUIModel(db, tuiCh, opts)
		p := tea.NewProgram(m, tea.WithAltScreen())
		go checkAndNotifyUpdate(p)
		if _, err := p.Run(); err != nil {
			log.Fatalf("tui error: %v", err)
		}
		return
	}

	logPath := defaultLogPath()
	restoreLogs, err := configureTUILogging(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure tui logging: %v\n", err)
		os.Exit(1)
	}
	defer restoreLogs()

	// Start embedded daemon
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		_ = restoreLogs()
		fmt.Fprintf(os.Stderr, "start daemon: %v\n", err)
		os.Exit(1)
	}
	defer d.Stop()
	daemon.WritePID()
	defer daemon.RemovePID()

	// Start Codex watcher (async emit decouples file parsing from DB writes)
	codexWatcher := collector.NewCodexWatcher(func(ev event.Event) {
		d.ProcessExternalEventAsync(ev)
	})
	collector.RegisterCodexWatcher(codexWatcher)
	codexWatcher.Start()
	defer codexWatcher.Stop()

	// Start Claude log watcher
	claudeLogWatcher := collector.NewClaudeLogWatcher(func(ev event.Event) {
		d.ProcessExternalEventAsync(ev)
	})
	claudeLogWatcher.Start()
	defer claudeLogWatcher.Stop()

	eventCh := d.Subscribe()

	// Forward daemon events to TUI.
	// Use a done channel so we can stop the goroutine before calling Unsubscribe,
	// preventing a race where broadcast sends to the channel after it is removed from subs.
	// The tuiCh send is non-blocking so the goroutine never stalls after the TUI exits.
	tuiCh := make(chan tui.EventMsg, 256)
	done := make(chan struct{})
	go func() {
		defer close(tuiCh)
		for {
			select {
			case _, ok := <-eventCh:
				if !ok {
					return
				}
				select {
				case tuiCh <- tui.EventMsg{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()

	m := newTUIModel(db, tuiCh, opts)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go checkAndNotifyUpdate(p)
	if _, err := p.Run(); err != nil {
		_ = restoreLogs()
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
	close(done)
	d.Unsubscribe(eventCh)
}

func runDaemon() {
	if maybePrintCmdHelp("daemon", os.Args[2:]) {
		return
	}
	if err := daemon.EnsureNotRunning(); err != nil {
		log.Fatalf("%v", err)
	}

	cleanupLogs, err := daemon.SetupLogFile()
	if err != nil {
		log.Fatalf("setup daemon log file: %v", err)
	}
	defer cleanupLogs()
	log.Printf("daemon log file enabled")

	db := mustOpenDB()
	defer db.Close()

	sockPath := daemon.DefaultSocketPath()
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		log.Fatalf("start daemon: %v", err)
	}
	daemon.WritePID()
	defer daemon.RemovePID()

	// Start Codex watcher (async emit decouples file parsing from DB writes)
	codexWatcher := collector.NewCodexWatcher(func(ev event.Event) {
		d.ProcessExternalEventAsync(ev)
	})
	collector.RegisterCodexWatcher(codexWatcher)
	codexWatcher.Start()
	defer codexWatcher.Stop()

	// Start Claude log watcher
	claudeLogWatcher := collector.NewClaudeLogWatcher(func(ev event.Event) {
		d.ProcessExternalEventAsync(ev)
	})
	claudeLogWatcher.Start()
	defer claudeLogWatcher.Stop()

	fmt.Printf("tokenmeter daemon running (socket: %s)\n", sockPath)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		sig := <-sigCh
		if sig == syscall.SIGHUP {
			d.ReloadConfig()
			continue
		}
		break
	}
	fmt.Fprintln(os.Stderr, "\nstopping daemon... (press Ctrl+C again to force quit)")

	// Watchdog goroutine: if Stop hangs, a second signal force-exits.
	go func() {
		for {
			sig := <-sigCh
			if sig == syscall.SIGHUP {
				d.ReloadConfig()
				continue
			}
			fmt.Fprintln(os.Stderr, "force quit")
			os.Exit(130)
		}
	}()

	// Stop watchers first so no new events are sent to the batch channel,
	// then stop daemon which drains remaining events before cleanup.
	claudeLogWatcher.Stop()
	codexWatcher.Stop()
	d.Stop()
	fmt.Println("daemon stopped")
}

func runEmit() {
	if maybePrintCmdHelp("emit", os.Args[2:]) {
		return
	}
	// Redirect log output to a dedicated file so emit errors don't pollute
	// Claude Code's hook stderr parsing. Open in append mode; if it fails,
	// silence logging entirely to avoid leaking anything to Claude's stderr.
	logPath := appdir.PathFor("emit.log", "emit.log")
	// Self-truncate at ~10MB so a crash loop can't fill the disk. emit.log is
	// a transient failure log, not an audit trail — losing history is fine.
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 10*1024*1024 {
		_ = os.Truncate(logPath, 0)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		log.SetOutput(f)
	} else {
		log.SetOutput(io.Discard)
	}

	if err := runEmitWithReader(daemon.DefaultSocketPath(), os.Stdin); err != nil {
		log.Printf("run emit: %v", err)
		// Exit 0 so Claude Code never treats hook failure as a tool failure.
		os.Exit(0)
	}
}

func runEmitWithReader(sockPath string, r io.Reader) error {
	hookEvent, err := collector.ParseClaudeHook(r)
	if err != nil {
		return err
	}

	// Use ClaudeHookToEvents which produces properly correlated events
	// using tool_use_id from Claude Code for Pre/Post matching
	events := collector.ClaudeHookToEvents(hookEvent)

	for _, ev := range events {
		if err := collector.EmitEvent(sockPath, ev); err != nil {
			// Daemon not running, silently fail
			return err
		}
	}
	return nil
}

// tokenmeter setup hook format:
// Claude Code settings.json uses: [{ "matcher": "", "hooks": [{ "type": "command", "command": "..." }] }]
func runSetup() {
	if maybePrintCmdHelp("setup", os.Args[2:]) {
		return
	}
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	var settings map[string]any
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			log.Fatalf("settings.json contains invalid JSON: %v\nPlease fix %s before running setup.", err, settingsPath)
		}
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	tokenmeterPath, _ := os.Executable()
	if tokenmeterPath == "" {
		tokenmeterPath = "tokenmeter"
	}
	// Quote the path if it contains spaces or quotes so the shell doesn't split it.
	quoted := tokenmeterPath
	if strings.ContainsAny(tokenmeterPath, " \t\"") {
		quoted = `"` + strings.ReplaceAll(tokenmeterPath, `"`, `\"`) + `"`
	}
	emitCmd := quoted + " emit"

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = make(map[string]any)
	}

	for _, hookName := range tokenmeterHookNames {
		removeTokenMeterHook(hooks, hookName)
		addHookEntry(hooks, hookName, emitCmd)
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Fatalf("marshal settings: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		log.Fatalf("create claude dir: %v", err)
	}

	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		log.Fatalf("write settings: %v", err)
	}

	fmt.Println("✓ Claude Code hooks configured")
	fmt.Printf("  Settings: %s\n", settingsPath)
	fmt.Printf("  Command:  %s\n", emitCmd)
	fmt.Printf("  Events:   %s\n", strings.Join(tokenmeterHookNames, ", "))
	fmt.Println()
	fmt.Println("Run `tokenmeter` to start monitoring.")
}

// addHookEntry adds a TokenMeter hook entry in the correct Claude Code format:
// [{ "matcher": "", "hooks": [{ "type": "command", "command": "..." }] }]
func addHookEntry(hooks map[string]any, hookName, emitCmd string) {
	tokenmeterHook := map[string]any{
		"type":    "command",
		"command": emitCmd,
	}

	matcherEntry := map[string]any{
		"matcher": "",
		"hooks":   []any{tokenmeterHook},
	}

	existing, ok := hooks[hookName].([]any)
	if ok {
		// Check if this exact hook already exists in any matcher entry.
		for _, entry := range existing {
			if entryMap, ok := entry.(map[string]any); ok {
				if innerHooks, ok := entryMap["hooks"].([]any); ok {
					for _, h := range innerHooks {
						if hm, ok := h.(map[string]any); ok {
							if cmd, ok := hm["command"].(string); ok && cmd == emitCmd {
								return // already installed
							}
						}
					}
				}
			}
		}
		hooks[hookName] = append(existing, matcherEntry)
	} else {
		hooks[hookName] = []any{matcherEntry}
	}
}

func runUninstall() {
	if maybePrintCmdHelp("uninstall", os.Args[2:]) {
		return
	}
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err == nil {
		var settings map[string]any
		if json.Unmarshal(data, &settings) == nil {
			if hooks, ok := settings["hooks"].(map[string]any); ok {
				for _, hookName := range tokenmeterHookNames {
					removeTokenMeterHook(hooks, hookName)
				}
				settings["hooks"] = hooks
				out, _ := json.MarshalIndent(settings, "", "  ")
				os.WriteFile(settingsPath, out, 0o644)
			}
		}
	}

	if running, pid := daemon.IsRunning(); running {
		proc, err := os.FindProcess(pid)
		if err == nil {
			proc.Signal(syscall.SIGTERM)
			fmt.Printf("✓ Stopped daemon (pid %d)\n", pid)
		}
	}

	fmt.Println("✓ Removed Claude Code hooks")
	fmt.Println()
	fmt.Printf("Data preserved at %s/\n", appdir.Base())
	fmt.Printf("To remove all data: rm -rf %s\n", appdir.Base())
}

// removeTokenMeterHook removes TokenMeter entries from the nested hook format.
func removeTokenMeterHook(hooks map[string]any, hookName string) {
	existing, ok := hooks[hookName].([]any)
	if !ok {
		return
	}

	var filtered []any
	for _, entry := range existing {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			filtered = append(filtered, entry)
			continue
		}

		innerHooks, ok := entryMap["hooks"].([]any)
		if !ok {
			filtered = append(filtered, entry)
			continue
		}

		// Filter out TokenMeter hooks from this matcher entry. During the rename,
		// old "agmon emit" hooks are also treated as TokenMeter hooks.
		var cleanHooks []any
		for _, h := range innerHooks {
			if hm, ok := h.(map[string]any); ok {
				if cmd, ok := hm["command"].(string); ok && isTokenMeterEmitCommand(cmd) {
					continue
				}
			}
			cleanHooks = append(cleanHooks, h)
		}

		if len(cleanHooks) > 0 {
			entryMap["hooks"] = cleanHooks
			filtered = append(filtered, entryMap)
		}
		// If no hooks left in this matcher entry, drop the whole entry
	}

	if len(filtered) > 0 {
		hooks[hookName] = filtered
	} else {
		delete(hooks, hookName)
	}
}

func isTokenMeterEmitCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasSuffix(cmd, " emit") {
		return false
	}
	return strings.Contains(cmd, "tokenmeter") || strings.Contains(cmd, "agmon")
}

func runReport() {
	if maybePrintCmdHelp("report", os.Args[2:]) {
		return
	}
	db := mustOpenDB()
	defer db.Close()

	// Check for --weekly or --monthly flag
	if len(os.Args) > 2 {
		switch os.Args[2] {
		case "--weekly":
			runPeriodReport(db, "weekly")
			return
		case "--monthly":
			runPeriodReport(db, "monthly")
			return
		}
	}

	target, ok := latestOrRequestedSession(db, os.Args)
	if !ok {
		fmt.Println("No sessions recorded.")
		return
	}

	name := target.SessionID
	if target.GitBranch != "" {
		name = target.GitBranch
	} else if target.CWD != "" {
		name = filepath.Base(target.CWD)
	}
	fmt.Printf("Session:  %s\n", name)
	fmt.Printf("ID:       %s\n", target.SessionID)
	fmt.Printf("Platform: %s\n", target.Platform)
	fmt.Printf("Status:   %s\n", target.Status)
	fmt.Printf("Started: %s\n", target.StartTime.Format("2006-01-02 15:04:05"))
	if target.EndTime != nil {
		fmt.Printf("Ended: %s\n", target.EndTime.Format("2006-01-02 15:04:05"))
		fmt.Printf("Duration: %s\n", target.EndTime.Sub(target.StartTime).Round(time.Second))
	}
	fmt.Printf("Tokens: %d in + %d out = %d total\n",
		target.TotalInputTokens, target.TotalOutputTokens,
		target.TotalInputTokens+target.TotalOutputTokens)
	fmt.Println()

	agents, _ := db.ListAgents(target.SessionID)
	if len(agents) > 0 {
		fmt.Println("Agents:")
		for _, a := range agents {
			prefix := "  "
			if a.ParentAgentID != "" {
				prefix = "    └─ "
			}
			status := "●"
			if a.Status == "ended" {
				status = "✓"
			}
			role := a.Role
			if role == "" {
				role = "main"
			}
			fmt.Printf("%s%s %s  %s\n", prefix, status, role, a.AgentID)
		}
		fmt.Println()
	}

	toolCalls, _ := db.ListToolCalls(target.SessionID, 50)
	if len(toolCalls) > 0 {
		fmt.Println("Tool Calls (last 50):")
		fmt.Printf("  %-8s %-15s %8s  %s\n", "TIME", "TOOL", "DURATION", "STATUS")
		for _, tc := range toolCalls {
			dur := fmt.Sprintf("%.1fs", float64(tc.DurationMs)/1000)
			if tc.DurationMs == 0 {
				dur = "-"
			}
			status := "✓"
			switch tc.Status {
			case "fail":
				status = "✗"
			case "pending":
				status = "…"
			case "retry":
				status = "↻"
			}
			fmt.Printf("  %-8s %-15s %8s  %s\n",
				tc.StartTime.Format("15:04:05"), tc.ToolName, dur, status)
		}
		fmt.Println()
	}

	fileChanges, _ := db.ListFileChanges(target.SessionID)
	if len(fileChanges) > 0 {
		fmt.Println("File Changes:")
		for _, fc := range fileChanges {
			icon := "~"
			switch fc.ChangeType {
			case "create":
				icon = "+"
			case "delete":
				icon = "-"
			}
			fmt.Printf("  %s %s\n", icon, fc.FilePath)
		}
	}
}

func runShare() {
	if maybePrintCmdHelp("share", os.Args[2:]) {
		return
	}
	db := mustOpenDB()
	defer db.Close()

	target, ok := latestOrRequestedSession(db, os.Args)
	if !ok {
		fmt.Println("No sessions recorded.")
		return
	}

	toolStats, _ := db.ListToolStats(target.SessionID)
	fileChanges, _ := db.ListFileChanges(target.SessionID)
	fmt.Print(report.SessionShareMarkdown(target, toolStats, fileChanges, time.Now().UTC()))
}

func runPeriodReport(db *storage.DB, period string) {
	// Local time boundaries align reports with the user's calendar — see
	// storage/query.go P2-15 for why DB aggregates use 'localtime'.
	now := time.Now()
	var from, to time.Time
	var title string

	switch period {
	case "weekly":
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		from = time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local)
		to = from.AddDate(0, 0, 7)
		if to.After(now) {
			to = now
		}
		title = fmt.Sprintf("Weekly Cost Report (%s ~ %s)", from.Format("2006-01-02"), now.Format("2006-01-02"))
	case "monthly":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		to = from.AddDate(0, 1, 0)
		if to.After(now) {
			to = now
		}
		title = fmt.Sprintf("Monthly Cost Report (%s)", from.Format("January 2006"))
	}

	totalCost, _ := db.GetCostBetween(from, to)
	dailyCosts, _ := db.GetDailyCostsBetween(from, to)
	modelCosts, _ := db.GetModelCostBreakdown(from, to)
	topSessions, _ := db.GetTopSessionsByCost(from, to, 10)

	// Output Markdown
	fmt.Printf("# %s\n\n", title)
	fmt.Printf("**Total Cost: $%.2f**\n\n", totalCost)

	// Daily breakdown
	if len(dailyCosts) > 0 {
		fmt.Print("## Daily Breakdown\n\n")
		fmt.Println("| Date | Cost |")
		fmt.Println("|------|------|")
		for _, dc := range dailyCosts {
			bar := ""
			if totalCost > 0 {
				pct := dc.Cost / totalCost
				barLen := int(pct * 20)
				if barLen > 0 {
					bar = " " + strings.Repeat("█", barLen)
				}
			}
			fmt.Printf("| %s | $%.2f%s |\n", dc.Date, dc.Cost, bar)
		}
		fmt.Println()
	}

	// Model breakdown
	if len(modelCosts) > 0 {
		fmt.Print("## Cost by Model\n\n")
		fmt.Println("| Model | Input Tokens | Output Tokens | Cost |")
		fmt.Println("|-------|-------------|---------------|------|")
		for _, mc := range modelCosts {
			fmt.Printf("| %s | %s | %s | $%.2f |\n",
				mc.Model, fmtTokens(mc.InputTokens), fmtTokens(mc.OutputTokens), mc.CostUSD)
		}
		fmt.Println()
	}

	// Top sessions
	if len(topSessions) > 0 {
		fmt.Print("## Top Sessions by Cost\n\n")
		fmt.Println("| Session | Platform | Input | Output | Cost |")
		fmt.Println("|---------|----------|-------|--------|------|")
		for _, ts := range topSessions {
			name := ts.SessionID
			if len(name) > 8 {
				name = name[:8]
			}
			if ts.GitBranch != "" {
				name = ts.GitBranch
			} else if ts.CWD != "" {
				name = filepath.Base(ts.CWD)
			}
			if len(name) > 30 {
				name = name[:30]
			}
			fmt.Printf("| %s | %s | %s | %s | $%.2f |\n",
				name, ts.Platform, fmtTokens(ts.InputTokens), fmtTokens(ts.OutputTokens), ts.CostUSD)
		}
		fmt.Println()
	}

	fmt.Printf("---\n*Generated by TokenMeter v%s at %s*\n", version, now.Format("2006-01-02 15:04:05 UTC"))
}

func runWeb() error {
	if maybePrintCmdHelp("web", os.Args[2:]) {
		return nil
	}
	webOpts, err := parseWebOptions(os.Args[2:])
	if err != nil {
		return err
	}
	if webOpts.generateToken {
		token, path, err := writeGeneratedWebToken()
		if err != nil {
			return fmt.Errorf("generate web token: %w", err)
		}
		fmt.Printf("Token written to %s\n", path)
		fmt.Printf("Set request header: Authorization: Bearer %s\n", token)
		return nil
	}
	authToken, err := resolveWebAuthToken(webOpts)
	if err != nil {
		return err
	}

	db := mustOpenDB()
	defer db.Close()

	sockPath := daemon.DefaultSocketPath()

	// Start embedded daemon if not already running, so web dashboard has live data.
	var d *daemon.Daemon
	if running, _ := daemon.IsRunning(); !running {
		d = daemon.New(db, sockPath)
		if err := d.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start daemon: %v\n", err)
		} else {
			daemon.WritePID()
			defer daemon.RemovePID()
			defer d.Stop()

			// Start Codex watcher
			codexWatcher := collector.NewCodexWatcher(func(ev event.Event) {
				d.ProcessExternalEventAsync(ev)
			})
			collector.RegisterCodexWatcher(codexWatcher)
			codexWatcher.Start()
			defer codexWatcher.Stop()

			// Start Claude log watcher
			claudeLogWatcher := collector.NewClaudeLogWatcher(func(ev event.Event) {
				d.ProcessExternalEventAsync(ev)
			})
			claudeLogWatcher.Start()
			defer claudeLogWatcher.Stop()

			fmt.Println("daemon + watchers started (live data collection)")
		}
	} else {
		fmt.Println("daemon already running (connecting to existing)")
	}

	opts := []web.ServerOption{
		web.WithEventSocketPath(sockPath),
		web.WithBuildVersion(version),
		web.WithAuthToken(authToken),
	}
	if d != nil {
		opts = append(opts, web.WithMetricsProvider(daemonMetricsProvider{daemon: d, db: db}))
	}
	srv := web.NewServer(db, webOpts.port, opts...)
	fmt.Printf("TokenMeter web dashboard: http://localhost:%s\n", webOpts.port)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("web server: %w", err)
			}
			return nil
		case sig := <-sigCh:
			if sig == syscall.SIGHUP {
				if d != nil {
					d.ReloadConfig()
				}
				continue
			}
			fmt.Println("\nshutting down web server... (press Ctrl+C again to force quit)")
			// Watchdog: second signal force-quits if Shutdown hangs.
			go func() {
				for {
					sig := <-sigCh
					if sig == syscall.SIGHUP {
						if d != nil {
							d.ReloadConfig()
						}
						continue
					}
					fmt.Fprintln(os.Stderr, "force quit")
					os.Exit(130)
				}
			}()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			if err := <-errCh; err != nil {
				return fmt.Errorf("web server shutdown: %w", err)
			}
			fmt.Println("web server stopped")
			return nil
		}
	}
}

func runStatus() {
	if maybePrintCmdHelp("status", os.Args[2:]) {
		return
	}
	db := mustOpenDB()
	defer db.Close()

	sessions, err := db.ListSessions()
	if err != nil {
		log.Fatalf("list sessions: %v", err)
	}

	activeCount := 0
	for _, s := range sessions {
		if s.Status == "active" {
			activeCount++
		}
	}

	todayIn, todayOut, _ := db.GetTodayTokens()
	todayCost, _ := db.GetTodayCost()
	fmt.Printf("Running: %d\n", activeCount)
	fmt.Printf("Today's tokens:  %s in / %s out\n", fmtTokens(todayIn), fmtTokens(todayOut))
	fmt.Printf("Today's cost:    $%.4f\n", todayCost)
	fmt.Println()

	if len(sessions) == 0 {
		fmt.Println("No sessions recorded.")
		return
	}

	fmt.Printf("%-24s %-8s %8s %8s  %8s  %s\n", "SESSION", "PLATFORM", "IN", "OUT", "COST", "STATUS")
	for _, s := range sessions {
		status := "●"
		switch s.Status {
		case "ended":
			status = "◌"
		case "stale":
			status = "?"
		}
		name := s.SessionID
		if s.GitBranch != "" {
			name = s.GitBranch
		} else if s.CWD != "" {
			name = filepath.Base(s.CWD)
		}
		if len(name) > 24 {
			name = name[:24]
		}
		fmt.Printf("%-24s %-8s %8s %8s  %8s  %s\n",
			name, s.Platform, fmtTokens(s.TotalInputTokens), fmtTokens(s.TotalOutputTokens),
			fmt.Sprintf("$%.2f", s.TotalCostUSD), status)
	}
}

func runCost() {
	if maybePrintCmdHelp("cost", os.Args[2:]) {
		return
	}
	db := mustOpenDB()
	defer db.Close()

	period := "today"
	if len(os.Args) > 2 {
		period = os.Args[2]
	}

	// Local time so "today/week/month" boundaries match the user's calendar
	// (DB aggregates with DATE(timestamp, 'localtime')).
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	var since *time.Time
	var label string
	switch period {
	case "today":
		t := startOfDay
		since, label = &t, "Today"
	case "week":
		wd := startOfDay.Weekday()
		if wd == 0 {
			wd = 7
		}
		t := startOfDay.AddDate(0, 0, -int(wd-1))
		since, label = &t, "This week"
	case "month":
		t := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		since, label = &t, "This month"
	case "3month":
		t := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -2, 0)
		since, label = &t, "Last 3 months"
	case "year":
		t := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.Local)
		since, label = &t, "This year"
	case "all":
		since, label = nil, "All time"
	default:
		fmt.Fprintf(os.Stderr, "Unknown period: %q (use today, week, month, 3month, year, all)\n", period)
		os.Exit(1)
	}

	in, out, _ := db.GetTokensSince(since)
	cost, _ := db.GetCostSince(since)
	fmt.Printf("%s:\n", label)
	fmt.Printf("  Tokens: %s in + %s out = %s total\n", fmtTokens(in), fmtTokens(out), fmtTokens(in+out))
	fmt.Printf("  Cost:   $%.4f\n", cost)
}

func runClean() {
	if maybePrintCmdHelp("clean", os.Args[2:]) {
		return
	}
	days := 7
	if len(os.Args) > 2 {
		d, err := strconv.Atoi(os.Args[2])
		if err != nil || d <= 0 {
			fmt.Fprintf(os.Stderr, "Invalid days: %q (must be a positive number)\n", os.Args[2])
			os.Exit(1)
		}
		days = d
	}

	db := mustOpenDB()
	defer db.Close()

	n, err := db.CleanOldSessions(days)
	if err != nil {
		log.Fatalf("clean: %v", err)
	}
	if n == 0 {
		fmt.Printf("No sessions older than %d days to remove.\n", days)
	} else {
		fmt.Printf("Removed %d session(s) older than %d days.\n", n, days)
	}
}

func runTag() {
	if maybePrintCmdHelp("tag", os.Args[2:]) {
		return
	}
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: tokenmeter tag <session-id> [text]\n")
		fmt.Fprintf(os.Stderr, "  Set a tag:   tokenmeter tag abc123 \"refactoring auth\"\n")
		fmt.Fprintf(os.Stderr, "  Clear tag:   tokenmeter tag abc123\n")
		os.Exit(1)
	}

	db := mustOpenDB()
	defer db.Close()

	prefix := os.Args[2]
	s, found, err := db.GetSessionByIDPrefix(prefix)
	if err != nil {
		log.Fatalf("lookup session: %v", err)
	}
	if !found {
		log.Fatalf("session not found: %s", prefix)
	}

	tag := ""
	if len(os.Args) > 3 {
		tag = strings.Join(os.Args[3:], " ")
	}

	if err := db.SetSessionTag(s.SessionID, tag); err != nil {
		log.Fatalf("set tag: %v", err)
	}

	if tag == "" {
		fmt.Printf("Cleared tag for session %s\n", shortSessionID(s.SessionID))
	} else {
		fmt.Printf("Tagged session %s: %s\n", shortSessionID(s.SessionID), tag)
	}
}

func shortSessionID(sessionID string) string {
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func fmtTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func checkAndNotifyUpdate(p *tea.Program) {
	rel, err := fetchLatestRelease()
	if err != nil {
		return
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest != version && version != "dev" {
		p.Send(tui.UpdateAvailableMsg(latest))
	}
}

type helpSection struct {
	title    string
	commands []helpCommand
}

type helpCommand struct {
	name string
	desc string
}

var helpSections = []helpSection{
	{"Setup & installation", []helpCommand{
		{"setup", "Configure Claude Code hooks"},
		{"uninstall", "Remove hooks and stop daemon"},
		{"init", "Interactive setup wizard"},
		{"doctor [--fix]", "Self-diagnostic and auto-repair"},
		{"completion <shell>", "Generate shell completion (bash|zsh|fish)"},
		{"update", "Update to latest release"},
		{"version [--check]", "Show version (and check for updates)"},
		{"help", "Show this help"},
	}},
	{"Run modes", []helpCommand{
		{"daemon", "Run daemon (foreground)"},
		{"web [--port N]", "Web dashboard at http://localhost:N"},
		{"watch [opts]", "Stream live events to stdout"},
		{"top [--once]", "Live dashboard snapshot"},
	}},
	{"Daily commands", []helpCommand{
		{"status", "Active session summary"},
		{"cost <period>", "Token/cost stats (today|week|month|3month|year|all)"},
		{"report [session]", "Detailed session report"},
		{"report --weekly", "Weekly Markdown cost report"},
		{"report --monthly", "Monthly Markdown cost report"},
		{"share [session]", "Shareable Markdown session recap"},
	}},
	{"Analysis", []helpCommand{
		{"analyze [--range]", "Usage insights with heatmap"},
		{"search <query>", "Search tool calls and file paths"},
		{"compare <a> <b>", "Diff two sessions"},
		{"export [opts]", "CSV/JSON export"},
	}},
	{"Maintenance", []helpCommand{
		{"clean [days]", "Remove sessions older than N days (default 7)"},
		{"compact [--full]", "PRAGMA optimize (or full VACUUM)"},
		{"checkpoint", "WAL truncate immediately"},
		{"backup [path]", "Snapshot database via VACUUM INTO"},
		{"restore <path>", "Restore from snapshot"},
		{"reload", "Send SIGHUP to running daemon"},
		{"logs [--follow]", "Tail daemon log"},
		{"healthcheck", "DB + daemon liveness probe"},
		{"emit", "Emit event from hook (reads stdin)"},
	}},
	{"Configuration", []helpCommand{
		{"tag <id> [text]", "Set/clear session note"},
		{"budget <subcommand>", "Manage budgets: list, set, delete, usage"},
		{"webhook <subcommand>", "Manage webhooks: list, test, replay"},
	}},
}

func printHelp() {
	fmt.Printf("TokenMeter v%s — AI coding agent usage meter\n\n", version)
	fmt.Println("Usage: tokenmeter <command> [args...]")
	fmt.Println("Or:    tokenmeter                  Launch TUI (auto-starts daemon)")
	fmt.Println("       tokenmeter --all            Launch TUI without workspace filtering")
	fmt.Println("       tokenmeter --workspace PATH Launch TUI scoped to a workspace path")
	fmt.Println()

	width := helpCommandWidth(helpSections)
	for _, section := range helpSections {
		fmt.Printf("▎%s\n", section.title)
		for _, cmd := range section.commands {
			fmt.Printf("  %-*s  %s\n", width, cmd.name, cmd.desc)
		}
		fmt.Println()
	}

	fmt.Println("▎Examples")
	for _, example := range []helpCommand{
		{"tokenmeter", "Launch TUI"},
		{"tokenmeter cost today", "Show today's tokens"},
		{"tokenmeter export --range week", "Export this week as CSV"},
		{"tokenmeter compare abc def", "Diff sessions by ID prefix"},
		{`tokenmeter budget set "Monthly" 100 --platform claude`, "Create a Claude monthly budget"},
	} {
		fmt.Printf("  %-*s  # %s\n", width, example.name, example.desc)
	}
	fmt.Println()
	fmt.Println("Run 'tokenmeter <command> --help' for command-specific options (if available).")
	fmt.Println("Source: https://github.com/tt-a1i/tokenmeter")
}

func helpCommandWidth(sections []helpSection) int {
	width := 0
	for _, section := range sections {
		for _, cmd := range section.commands {
			if len(cmd.name) > width {
				width = len(cmd.name)
			}
		}
	}
	return width
}
