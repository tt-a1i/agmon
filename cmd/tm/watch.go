package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

type watchOptions struct {
	sessionPrefix string
	sessionID     string
	typeFilters   map[string]bool
	color         bool
}

type watchSubscribeFunc func(string) (<-chan event.Event, func(), error)

func runWatch() error {
	if maybePrintCmdHelp("watch", os.Args[2:]) {
		return nil
	}
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopCh)

	return runWatchWithDeps(os.Args[2:], os.Stdout, os.Stderr, daemon.DefaultSocketPath(), daemon.SubscribeRemote, stopCh)
}

func runWatchWithDeps(args []string, out, errOut io.Writer, sockPath string, subscribe watchSubscribeFunc, stopCh <-chan os.Signal) error {
	opts, err := parseWatchArgs(args)
	if err != nil {
		return err
	}
	if opts.sessionPrefix != "" {
		sessionID, err := resolveWatchSession(opts.sessionPrefix)
		if err != nil {
			return err
		}
		opts.sessionID = sessionID
	}

	events, closeFn, err := subscribe(sockPath)
	if err != nil {
		return fmt.Errorf("Error: daemon not running. Start with 'tokenmeter daemon' or 'tokenmeter'.")
	}
	defer closeFn()

	fmt.Fprintln(errOut, "Watching events. Ctrl+C to exit.")
	for {
		select {
		case <-stopCh:
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if !matchWatchFilters(ev, opts) {
				continue
			}
			fmt.Fprintln(out, formatWatchEvent(ev, opts.color))
		}
	}
}

func parseWatchArgs(args []string) (watchOptions, error) {
	opts := watchOptions{color: true}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--session requires a value")
			}
			if opts.sessionPrefix != "" {
				return opts, fmt.Errorf("session filter provided more than once")
			}
			opts.sessionPrefix = args[i+1]
			i++
		case "--types":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--types requires a value")
			}
			filters, err := parseWatchTypes(args[i+1])
			if err != nil {
				return opts, err
			}
			opts.typeFilters = filters
			i++
		case "--no-color":
			opts.color = false
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown watch argument: %s", args[i])
			}
			if opts.sessionPrefix != "" {
				return opts, fmt.Errorf("session filter provided more than once")
			}
			opts.sessionPrefix = args[i]
		}
	}
	return opts, nil
}

func parseWatchTypes(raw string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		canonical, ok := normalizeWatchTypeName(name)
		if !ok {
			return nil, fmt.Errorf("unknown watch event type %q (use tool, token, file, session, agent)", part)
		}
		result[canonical] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("--types requires at least one event type")
	}
	return result, nil
}

func normalizeWatchTypeName(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "tool", "tools", "tool_call_start", "tool_call_end", "pretooluse", "posttooluse":
		return "tool", true
	case "token", "tokens", "token_usage", "tokenusage":
		return "token", true
	case "file", "files", "file_change", "filechange":
		return "file", true
	case "session", "sessions", "session_start", "session_update", "session_end", "sessionstart", "sessionupdate", "sessionend":
		return "session", true
	case "agent", "agents", "agent_start", "agent_end", "subagentstart", "subagentstop":
		return "agent", true
	default:
		return "", false
	}
}

func resolveWatchSession(prefix string) (string, error) {
	db, err := storage.Open(storage.DefaultDBPath())
	if err != nil {
		return "", fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	s, found, err := db.GetSessionByIDPrefix(prefix)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("session not found: %s", prefix)
	}
	return s.SessionID, nil
}

func matchWatchFilters(ev event.Event, opts watchOptions) bool {
	if opts.sessionID != "" && ev.SessionID != opts.sessionID {
		return false
	}
	if len(opts.typeFilters) > 0 && !opts.typeFilters[watchEventCategory(ev.Type)] {
		return false
	}
	return true
}

func watchEventCategory(t event.EventType) string {
	switch t {
	case event.EventToolCallStart, event.EventToolCallEnd:
		return "tool"
	case event.EventTokenUsage:
		return "token"
	case event.EventFileChange:
		return "file"
	case event.EventSessionStart, event.EventSessionUpdate, event.EventSessionEnd:
		return "session"
	case event.EventAgentStart, event.EventAgentEnd:
		return "agent"
	default:
		return string(t)
	}
}

func formatWatchEvent(ev event.Event, color bool) string {
	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	line := fmt.Sprintf("[%s] %-8s %-7s %-12s %s",
		ts.Local().Format("15:04:05"),
		shortSessionID(ev.SessionID),
		string(ev.Platform),
		watchEventLabel(ev.Type),
		watchEventDetail(ev),
	)
	line = strings.TrimRight(line, " ")
	if !color {
		return line
	}
	return colorWatchLine(line, ev.Type)
}

func watchEventLabel(t event.EventType) string {
	switch t {
	case event.EventToolCallStart:
		return "PreToolUse"
	case event.EventToolCallEnd:
		return "PostToolUse"
	case event.EventTokenUsage:
		return "TokenUsage"
	case event.EventFileChange:
		return "FileChange"
	case event.EventSessionStart:
		return "SessionStart"
	case event.EventSessionUpdate:
		return "SessionUpdate"
	case event.EventSessionEnd:
		return "SessionEnd"
	case event.EventAgentStart:
		return "SubagentStart"
	case event.EventAgentEnd:
		return "SubagentStop"
	default:
		return string(t)
	}
}

func watchEventDetail(ev event.Event) string {
	switch ev.Type {
	case event.EventToolCallStart:
		return strings.TrimSpace(ev.Data.ToolName + " " + watchToolTarget(ev.Data))
	case event.EventToolCallEnd:
		status := string(ev.Data.ToolStatus)
		if status == "" {
			status = "done"
		}
		toolName := ev.Data.ToolName
		if toolName == "" {
			toolName = "tool"
		}
		return fmt.Sprintf("%s (%s, %s)", toolName, formatWatchDuration(ev.Data.DurationMs), status)
	case event.EventTokenUsage:
		return fmt.Sprintf("in=%d out=%d cost=$%.3f", ev.Data.InputTokens, ev.Data.OutputTokens, ev.Data.CostUSD)
	case event.EventFileChange:
		return strings.TrimSpace(string(ev.Data.ChangeType) + " " + ev.Data.FilePath)
	case event.EventSessionStart, event.EventSessionUpdate:
		return watchSessionDetail(ev.Data)
	case event.EventSessionEnd:
		return "ended"
	case event.EventAgentStart, event.EventAgentEnd:
		if ev.Data.AgentRole != "" {
			return ev.Data.AgentRole
		}
		return ev.AgentID
	default:
		return ""
	}
}

func watchToolTarget(data event.EventData) string {
	if data.FilePath != "" {
		return data.FilePath
	}
	if data.ToolParams == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(data.ToolParams), &params); err != nil {
		return truncateWatchText(data.ToolParams, 80)
	}
	for _, key := range []string{"file_path", "path", "command", "pattern"} {
		if value, ok := params[key].(string); ok && value != "" {
			return truncateWatchText(value, 80)
		}
	}
	return truncateWatchText(data.ToolParams, 80)
}

func watchSessionDetail(data event.EventData) string {
	if data.CWD == "" && data.GitBranch == "" {
		return ""
	}
	if data.CWD == "" {
		return data.GitBranch
	}
	if data.GitBranch == "" {
		return data.CWD
	}
	return data.CWD + " (" + data.GitBranch + ")"
}

func formatWatchDuration(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func colorWatchLine(line string, t event.EventType) string {
	color := ""
	switch t {
	case event.EventToolCallStart, event.EventToolCallEnd:
		color = "\x1b[36m"
	case event.EventTokenUsage:
		color = "\x1b[33m"
	case event.EventFileChange:
		color = "\x1b[32m"
	case event.EventSessionStart, event.EventSessionUpdate, event.EventSessionEnd:
		color = "\x1b[2m"
	}
	if color == "" {
		return line
	}
	return color + line + "\x1b[0m"
}

func truncateWatchText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
