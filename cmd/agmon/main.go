package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/daemon"
	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
	"github.com/tt-a1i/agmon/internal/tui"
)

const version = "0.3.1"

var agmonHookNames = []string{
	"SessionStart", "SessionEnd", "Stop",
	"PreToolUse", "PostToolUse", "PostToolUseFailure",
	"SubagentStart", "SubagentStop",
}

func mustOpenDB() *storage.DB {
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	return db
}

func main() {
	if len(os.Args) < 2 {
		runTUI()
		return
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "emit":
		runEmit()
	case "setup":
		runSetup()
	case "uninstall":
		runUninstall()
	case "status":
		runStatus()
	case "report":
		runReport()
	case "cost":
		runCost()
	case "clean":
		runClean()
	case "version", "-v", "--version":
		fmt.Printf("agmon v%s\n", version)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func runTUI() {
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

		m := tui.NewModel(db, tuiCh)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			log.Fatalf("tui error: %v", err)
		}
		return
	}

	// Start embedded daemon
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		log.Fatalf("start daemon: %v", err)
	}
	defer d.Stop()
	daemon.WritePID()
	defer daemon.RemovePID()

	// Start Codex watcher
	codexWatcher := collector.NewCodexWatcher(func(ev event.Event) {
		d.ProcessExternalEvent(ev)
	})
	codexWatcher.Start()
	defer codexWatcher.Stop()

	// Start Claude log watcher
	claudeLogWatcher := collector.NewClaudeLogWatcher(func(ev event.Event) {
		d.ProcessExternalEvent(ev)
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

	m := tui.NewModel(db, tuiCh)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui error: %v", err)
	}
	close(done)
	d.Unsubscribe(eventCh)
}

func runDaemon() {
	if err := daemon.EnsureNotRunning(); err != nil {
		log.Fatalf("%v", err)
	}

	db := mustOpenDB()
	defer db.Close()

	sockPath := daemon.DefaultSocketPath()
	d := daemon.New(db, sockPath)
	if err := d.Start(); err != nil {
		log.Fatalf("start daemon: %v", err)
	}
	daemon.WritePID()
	defer daemon.RemovePID()

	// Start Codex watcher
	codexWatcher := collector.NewCodexWatcher(func(ev event.Event) {
		d.ProcessExternalEvent(ev)
	})
	codexWatcher.Start()
	defer codexWatcher.Stop()

	// Start Claude log watcher
	claudeLogWatcher := collector.NewClaudeLogWatcher(func(ev event.Event) {
		d.ProcessExternalEvent(ev)
	})
	claudeLogWatcher.Start()
	defer claudeLogWatcher.Stop()

	fmt.Printf("agmon daemon running (socket: %s)\n", sockPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	d.Stop()
	fmt.Println("\ndaemon stopped")
}

func runEmit() {
	hookEvent, err := collector.ParseClaudeHookStdin()
	if err != nil {
		os.Exit(0)
	}

	// Use ClaudeHookToEvents which produces properly correlated events
	// using tool_use_id from Claude Code for Pre/Post matching
	events := collector.ClaudeHookToEvents(hookEvent)

	sockPath := daemon.DefaultSocketPath()
	for _, ev := range events {
		if err := collector.EmitEvent(sockPath, ev); err != nil {
			// Daemon not running, silently fail
			os.Exit(0)
		}
	}
}

// agmon setup hook format:
// Claude Code settings.json uses: [{ "matcher": "", "hooks": [{ "type": "command", "command": "..." }] }]
func runSetup() {
	home, _ := os.UserHomeDir()
	settingsPath := home + "/.claude/settings.json"

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

	agmonPath, _ := os.Executable()
	if agmonPath == "" {
		agmonPath = "agmon"
	}
	emitCmd := fmt.Sprintf("%s emit", agmonPath)

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = make(map[string]any)
	}

	for _, hookName := range agmonHookNames {
		addHookEntry(hooks, hookName, emitCmd)
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Fatalf("marshal settings: %v", err)
	}

	if err := os.MkdirAll(home+"/.claude", 0o755); err != nil {
		log.Fatalf("create claude dir: %v", err)
	}

	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		log.Fatalf("write settings: %v", err)
	}

	fmt.Println("✓ Claude Code hooks configured")
	fmt.Printf("  Settings: %s\n", settingsPath)
	fmt.Printf("  Command:  %s\n", emitCmd)
	fmt.Printf("  Events:   %s\n", strings.Join(agmonHookNames, ", "))
	fmt.Println()
	fmt.Println("Run `agmon` to start monitoring.")
}

// addHookEntry adds an agmon hook entry in the correct Claude Code format:
// [{ "matcher": "", "hooks": [{ "type": "command", "command": "..." }] }]
func addHookEntry(hooks map[string]any, hookName, emitCmd string) {
	agmonHook := map[string]any{
		"type":    "command",
		"command": emitCmd,
	}

	matcherEntry := map[string]any{
		"matcher": "",
		"hooks":   []any{agmonHook},
	}

	existing, ok := hooks[hookName].([]any)
	if ok {
		// Check if agmon hook already exists in any matcher entry
		for _, entry := range existing {
			if entryMap, ok := entry.(map[string]any); ok {
				if innerHooks, ok := entryMap["hooks"].([]any); ok {
					for _, h := range innerHooks {
						if hm, ok := h.(map[string]any); ok {
							if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, "agmon") {
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
	home, _ := os.UserHomeDir()
	settingsPath := home + "/.claude/settings.json"

	data, err := os.ReadFile(settingsPath)
	if err == nil {
		var settings map[string]any
		if json.Unmarshal(data, &settings) == nil {
			if hooks, ok := settings["hooks"].(map[string]any); ok {
				for _, hookName := range agmonHookNames {
					removeAgmonHook(hooks, hookName)
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
	fmt.Println("Data preserved at ~/.agmon/")
	fmt.Println("To remove all data: rm -rf ~/.agmon")
}

// removeAgmonHook removes agmon entries from the nested hook format.
func removeAgmonHook(hooks map[string]any, hookName string) {
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

		// Filter out agmon hooks from this matcher entry
		var cleanHooks []any
		for _, h := range innerHooks {
			if hm, ok := h.(map[string]any); ok {
				if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, "agmon") {
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

func runReport() {
	db := mustOpenDB()
	defer db.Close()

	sessions, err := db.ListSessions()
	if err != nil {
		log.Fatalf("list sessions: %v", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions recorded.")
		return
	}

	var target storage.SessionRow
	if len(os.Args) > 2 {
		sid := os.Args[2]
		for _, s := range sessions {
			if strings.HasPrefix(s.SessionID, sid) {
				target = s
				break
			}
		}
		if target.SessionID == "" {
			log.Fatalf("session not found: %s", sid)
		}
	} else {
		target = sessions[0]
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

func runStatus() {
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
	db := mustOpenDB()
	defer db.Close()

	period := "today"
	if len(os.Args) > 2 {
		period = os.Args[2]
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var since *time.Time
	var label string
	switch period {
	case "today":
		t := startOfDay
		since, label = &t, "Today"
	case "week":
		t := startOfDay.AddDate(0, 0, -7)
		since, label = &t, "This week"
	case "month":
		t := startOfDay.AddDate(0, -1, 0)
		since, label = &t, "This month"
	case "3month":
		t := startOfDay.AddDate(0, -3, 0)
		since, label = &t, "Last 3 months"
	case "year":
		t := startOfDay.AddDate(-1, 0, 0)
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

func fmtTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func printHelp() {
	fmt.Printf(`agmon v%s - AI Agent Monitor

Usage:
  agmon                    Start TUI (auto-starts daemon)
  agmon daemon             Start daemon only
  agmon emit               Emit event from hook (reads stdin)
  agmon setup              Configure Claude Code hooks
  agmon uninstall          Remove hooks and stop daemon
  agmon status             Show active sessions summary
  agmon report [session]   Detailed session report
  agmon cost [today|week]  Token usage statistics
  agmon clean [days]       Remove sessions older than N days (default: 7)
  agmon version            Show version
  agmon help               Show this help
`, version)
}
