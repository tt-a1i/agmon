package main

import (
	"fmt"
	"os"
	"strings"
)

type cmdHelp struct {
	name        string
	short       string
	usage       string
	description string
	options     []optionHelp
	examples    []string
	seeAlso     []string
}

type optionHelp struct {
	flag string
	desc string
}

var cmdHelps = map[string]cmdHelp{
	"setup": {
		name:        "setup",
		short:       "Configure Claude Code hooks",
		usage:       "tm setup",
		description: "Configure Claude Code hooks so TokenMeter receives session, tool, and token events.",
		examples:    []string{"tm setup"},
		seeAlso:     []string{"init", "doctor"},
	},
	"uninstall": {
		name:        "uninstall",
		short:       "Remove hooks and stop daemon",
		usage:       "tm uninstall",
		description: "Remove TokenMeter hooks from Claude Code settings and stop the local daemon.",
		examples:    []string{"tm uninstall"},
		seeAlso:     []string{"setup", "doctor"},
	},
	"init": {
		name:        "init",
		short:       "Interactive setup wizard",
		usage:       "tm init [options]",
		description: "Run the first-time setup wizard for hooks, optional budgets, webhooks, pricing guidance, and diagnostics.",
		options:     []optionHelp{{"--skip-prompts", "use defaults without interactive prompts"}},
		examples:    []string{"tm init", "tm init --skip-prompts"},
		seeAlso:     []string{"setup", "doctor"},
	},
	"doctor": {
		name:        "doctor",
		short:       "Self-diagnostic and auto-repair",
		usage:       "tm doctor [options]",
		description: "Run installation diagnostics for hooks, daemon state, sockets, database health, and config files.",
		options: []optionHelp{
			{"--json", "output diagnostics as JSON"},
			{"--fix", "attempt safe automatic repairs"},
		},
		examples: []string{"tm doctor", "tm doctor --fix", "tm doctor --fix --json"},
		seeAlso:  []string{"init", "setup"},
	},
	"completion": {
		name:        "completion",
		short:       "Generate shell completion (bash|zsh|fish)",
		usage:       "tm completion bash|zsh|fish",
		description: "Print a shell completion script to stdout. Source it directly or redirect it to your shell completion directory.",
		examples:    []string{"tm completion bash", "source <(tm completion zsh)", "tm completion fish > ~/.config/fish/completions/tm.fish"},
		seeAlso:     []string{"help"},
	},
	"update": {
		name:        "update",
		short:       "Update to latest release",
		usage:       "tm update",
		description: "Download and install the latest TokenMeter release for the current platform.",
		examples:    []string{"tm update"},
		seeAlso:     []string{"version"},
	},
	"version": {
		name:        "version",
		short:       "Show version (and check for updates)",
		usage:       "tm version [options]",
		description: "Show the current TokenMeter version and optionally check GitHub for a newer release.",
		options:     []optionHelp{{"--check", "check whether a newer release is available"}},
		examples:    []string{"tm version", "tm version --check"},
		seeAlso:     []string{"update"},
	},
	"help": {
		name:        "help",
		short:       "Show top-level help",
		usage:       "tm help",
		description: "Show grouped top-level help with all TokenMeter command categories.",
		examples:    []string{"tm help", "tm -h"},
		seeAlso:     []string{"completion"},
	},
	"daemon": {
		name:        "daemon",
		short:       "Run daemon (foreground)",
		usage:       "tm daemon",
		description: "Run the TokenMeter daemon in the foreground. The daemon receives hook events and stores usage in the local database.",
		examples:    []string{"tm daemon"},
		seeAlso:     []string{"status", "healthcheck"},
	},
	"web": {
		name:        "web",
		short:       "Web dashboard at http://localhost:N",
		usage:       "tm web [options]",
		description: "Start the local web dashboard.",
		options: []optionHelp{
			{"--port N", "listen on port N (default: 8370)"},
			{"--host HOST", "bind to HOST when supported"},
		},
		examples: []string{"tm web", "tm web --port 9000"},
		seeAlso:  []string{"top", "watch"},
	},
	"watch": {
		name:        "watch",
		short:       "Stream live events to stdout",
		usage:       "tm watch [session-id] [options]",
		description: "Stream live daemon events to stdout, optionally filtered by session or event type.",
		options: []optionHelp{
			{"--session PREFIX", "only show events for one session prefix"},
			{"--types LIST", "comma-separated event groups: tool, token, file, session, agent"},
			{"--no-color", "disable ANSI color output"},
		},
		examples: []string{"tm watch", "tm watch --types tool,token", "tm watch --session abc123"},
		seeAlso:  []string{"daemon", "top"},
	},
	"top": {
		name:        "top",
		short:       "Live dashboard snapshot",
		usage:       "tm top [options]",
		description: "Show a minimal live usage dashboard in the terminal.",
		options: []optionHelp{
			{"--once", "print one snapshot and exit"},
			{"--no-clear", "do not clear the screen between refreshes"},
			{"--interval DURATION", "refresh interval such as 2s"},
		},
		examples: []string{"tm top", "tm top --once"},
		seeAlso:  []string{"watch", "status"},
	},
	"status": {
		name:        "status",
		short:       "Active session summary",
		usage:       "tm status",
		description: "Show active sessions plus today's token and cost totals.",
		examples:    []string{"tm status"},
		seeAlso:     []string{"cost", "report"},
	},
	"cost": {
		name:        "cost",
		short:       "Token/cost stats",
		usage:       "tm cost [period]",
		description: "Show token and cost totals for a period.",
		options:     []optionHelp{{"period", "today | week | month | 3month | year | all (default: today)"}},
		examples:    []string{"tm cost today", "tm cost month", "tm cost all"},
		seeAlso:     []string{"analyze", "export"},
	},
	"report": {
		name:        "report",
		short:       "Detailed session report",
		usage:       "tm report [session] [options]",
		description: "Show a detailed report for a session, or emit weekly/monthly Markdown summaries.",
		options: []optionHelp{
			{"--weekly", "generate weekly Markdown cost report"},
			{"--monthly", "generate monthly Markdown cost report"},
		},
		examples: []string{"tm report", "tm report abc123", "tm report --weekly"},
		seeAlso:  []string{"share", "compare"},
	},
	"share": {
		name:        "share",
		short:       "Shareable Markdown session recap",
		usage:       "tm share [session]",
		description: "Print a shareable Markdown recap for the latest or requested session.",
		examples:    []string{"tm share", "tm share abc123"},
		seeAlso:     []string{"report", "compare"},
	},
	"analyze": {
		name:        "analyze",
		short:       "Usage insights with heatmap",
		usage:       "tm analyze [options]",
		description: "Summarize AI agent usage, cost, sessions, models, tools, files, and activity heatmap.",
		options: []optionHelp{
			{"--range RANGE", "week | month | all (default: month)"},
			{"--json", "output machine-readable JSON"},
		},
		examples: []string{"tm analyze", "tm analyze --range week", "tm analyze --json"},
		seeAlso:  []string{"cost", "export"},
	},
	"search": {
		name:        "search",
		short:       "Search tool calls and file paths",
		usage:       "tm search <query> [options]",
		description: "Search tool parameters, tool results, and file paths stored in the local database.",
		options:     []optionHelp{{"--limit N", "maximum matches to print (default: 20)"}},
		examples:    []string{"tm search Edit", "tm search src/internal --limit 10"},
		seeAlso:     []string{"report", "export"},
	},
	"compare": {
		name:        "compare",
		short:       "Diff two sessions",
		usage:       "tm compare <sessionA> <sessionB> [options]",
		description: "Compare two sessions by ID or prefix, including cost, tool calls, touched files, and top tool differences.",
		options:     []optionHelp{{"--format FORMAT", "text | json (default: text)"}},
		examples:    []string{"tm compare abc def", "tm compare abc def --format json"},
		seeAlso:     []string{"report", "share"},
	},
	"export": {
		name:        "export",
		short:       "CSV/JSON export",
		usage:       "tm export [options]",
		description: "Export session/token data as CSV or JSON to stdout or a file.\nData includes per-row date, session, platform, model, tokens, and cost.",
		options: []optionHelp{
			{"--range RANGE", "today | week | month | all (default: week)"},
			{"--format FORMAT", "csv | json (default: csv)"},
			{"--out FILE", "write to FILE instead of stdout"},
		},
		examples: []string{"tm export", "tm export --range month --format json", "tm export --out /tmp/october.csv"},
		seeAlso:  []string{"compare", "share"},
	},
	"clean": {
		name:        "clean",
		short:       "Remove old sessions",
		usage:       "tm clean [days]",
		description: "Remove sessions older than N days from the local database.",
		examples:    []string{"tm clean", "tm clean 30"},
		seeAlso:     []string{"backup", "compact"},
	},
	"compact": {
		name:        "compact",
		short:       "PRAGMA optimize or full VACUUM",
		usage:       "tm compact [options]",
		description: "Analyze database health and run SQLite optimization, or full VACUUM when requested.",
		options:     []optionHelp{{"--full", "run full VACUUM"}},
		examples:    []string{"tm compact", "tm compact --full"},
		seeAlso:     []string{"checkpoint", "backup"},
	},
	"checkpoint": {
		name:        "checkpoint",
		short:       "WAL truncate immediately",
		usage:       "tm checkpoint",
		description: "Run an immediate SQLite WAL checkpoint/truncate for the local database.",
		examples:    []string{"tm checkpoint"},
		seeAlso:     []string{"compact", "doctor"},
	},
	"backup": {
		name:        "backup",
		short:       "Snapshot database via VACUUM INTO",
		usage:       "tm backup [path]",
		description: "Create a SQLite database snapshot. If no path is provided, TokenMeter writes to the backups directory.",
		examples:    []string{"tm backup", "tm backup /tmp/tokenmeter.db"},
		seeAlso:     []string{"restore", "clean"},
	},
	"restore": {
		name:        "restore",
		short:       "Restore from snapshot",
		usage:       "tm restore <path>",
		description: "Restore the local database from a backup snapshot after creating a pre-restore backup.",
		examples:    []string{"tm restore ~/.tokenmeter/backups/tokenmeter.db"},
		seeAlso:     []string{"backup", "doctor"},
	},
	"reload": {
		name:        "reload",
		short:       "Send SIGHUP to running daemon",
		usage:       "tm reload",
		description: "Ask the running daemon to reload its configuration.",
		examples:    []string{"tm reload"},
		seeAlso:     []string{"daemon", "healthcheck"},
	},
	"logs": {
		name:        "logs",
		short:       "Tail daemon log",
		usage:       "tm logs [options]",
		description: "Print or follow daemon and hook emit logs.",
		options: []optionHelp{
			{"--follow", "follow log output"},
			{"--emit", "show hook emit logs"},
			{"--path PATH", "read a specific log path"},
			{"--lines N", "number of lines to show"},
		},
		examples: []string{"tm logs", "tm logs --follow", "tm logs --lines 100"},
		seeAlso:  []string{"doctor", "daemon"},
	},
	"healthcheck": {
		name:        "healthcheck",
		short:       "DB + daemon liveness probe",
		usage:       "tm healthcheck [options]",
		description: "Check local database and daemon liveness for scripts and probes.",
		options:     []optionHelp{{"--json", "output machine-readable JSON"}},
		examples:    []string{"tm healthcheck", "tm healthcheck --json"},
		seeAlso:     []string{"doctor", "status"},
	},
	"emit": {
		name:        "emit",
		short:       "Emit event from hook",
		usage:       "tm emit",
		description: "Read a hook event from stdin and send it to the local daemon. This command is normally called by configured hooks.",
		examples:    []string{"tm emit < event.json"},
		seeAlso:     []string{"setup", "daemon"},
	},
	"tag": {
		name:        "tag",
		short:       "Set/clear session note",
		usage:       "tm tag <session-id> [text]",
		description: "Set or clear a note/tag on a session by ID prefix.",
		examples:    []string{`tm tag abc123 "refactoring auth"`, "tm tag abc123"},
		seeAlso:     []string{"report", "search"},
	},
	"budget": {
		name:        "budget",
		short:       "Manage budgets",
		usage:       "tm budget <list|set|delete|usage> [args]",
		description: "Manage monthly budgets and inspect usage against configured limits.",
		options:     []optionHelp{{"--platform PLATFORM", "for set: claude | codex"}},
		examples:    []string{"tm budget list", `tm budget set "Monthly" 100 --platform claude`, "tm budget usage 1"},
		seeAlso:     []string{"doctor", "webhook"},
	},
	"webhook": {
		name:        "webhook",
		short:       "Manage webhooks",
		usage:       "tm webhook <list|test|replay> [args]",
		description: "List, test, and replay webhook endpoints used for budget and daemon notifications.",
		examples:    []string{"tm webhook list", "tm webhook test https://example.com/hook", "tm webhook replay"},
		seeAlso:     []string{"budget", "doctor"},
	},
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func maybePrintCmdHelp(name string, args []string) bool {
	if !hasHelpFlag(args) {
		return false
	}
	if err := printCmdHelp(name); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return true
}

func printCmdHelp(name string) error {
	help, ok := cmdHelps[name]
	if !ok {
		return fmt.Errorf("no help for %q", name)
	}
	fmt.Printf("Usage: %s\n\n", help.usage)
	fmt.Println(help.description)
	if len(help.options) > 0 {
		fmt.Println()
		fmt.Println("Options:")
		width := optionHelpWidth(help.options)
		for _, opt := range help.options {
			fmt.Printf("  %-*s  %s\n", width, opt.flag, opt.desc)
		}
	}
	if len(help.examples) > 0 {
		fmt.Println()
		fmt.Println("Examples:")
		for _, example := range help.examples {
			fmt.Printf("  %s\n", example)
		}
	}
	if len(help.seeAlso) > 0 {
		fmt.Println()
		refs := make([]string, 0, len(help.seeAlso))
		for _, ref := range help.seeAlso {
			refs = append(refs, "tm "+ref)
		}
		fmt.Printf("See also: %s\n", strings.Join(refs, ", "))
	}
	return nil
}

func optionHelpWidth(options []optionHelp) int {
	width := 0
	for _, opt := range options {
		if len(opt.flag) > width {
			width = len(opt.flag)
		}
	}
	return width
}

func unknownCommandHelpMessage(cmd string) string {
	return fmt.Sprintf("unknown command: %s\nRun 'tm help' to list commands.\n", cmd)
}
