package main

import (
	"fmt"
	"os"
	"strings"
)

var completionSubcommands = []string{
	"daemon",
	"reload",
	"emit",
	"setup",
	"init",
	"uninstall",
	"status",
	"report",
	"share",
	"cost",
	"export",
	"compare",
	"search",
	"budget",
	"webhook",
	"analyze",
	"watch",
	"top",
	"healthcheck",
	"logs",
	"backup",
	"restore",
	"doctor",
	"compact",
	"web",
	"clean",
	"tag",
	"update",
	"version",
	"help",
	"completion",
}

func runCompletion() error {
	if maybePrintCmdHelp("completion", os.Args[2:]) {
		return nil
	}
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: tm completion bash|zsh|fish")
	}
	switch os.Args[2] {
	case "bash":
		fmt.Print(bashCompletionScript())
	case "zsh":
		fmt.Print(zshCompletionScript())
	case "fish":
		fmt.Print(fishCompletionScript())
	default:
		return fmt.Errorf("unknown shell: %s", os.Args[2])
	}
	return nil
}

func bashCompletionScript() string {
	subcmds := strings.Join(completionSubcommands, " ")
	return fmt.Sprintf(`_tm() {
    local cur prev subcmds
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    subcmds="%s"

    case "$prev" in
        tm)
            COMPREPLY=( $(compgen -W "$subcmds" -- "$cur") )
            return 0 ;;
        budget)
            COMPREPLY=( $(compgen -W "list set delete usage" -- "$cur") )
            return 0 ;;
        webhook)
            COMPREPLY=( $(compgen -W "list test replay" -- "$cur") )
            return 0 ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
            return 0 ;;
        cost)
            COMPREPLY=( $(compgen -W "today week month 3month year all" -- "$cur") )
            return 0 ;;
        report)
            COMPREPLY=( $(compgen -W "--weekly --monthly" -- "$cur") )
            return 0 ;;
        export)
            COMPREPLY=( $(compgen -W "--range --format --out" -- "$cur") )
            return 0 ;;
        analyze)
            COMPREPLY=( $(compgen -W "--range --json" -- "$cur") )
            return 0 ;;
        watch)
            COMPREPLY=( $(compgen -W "--session --types --no-color" -- "$cur") )
            return 0 ;;
        doctor)
            COMPREPLY=( $(compgen -W "--json --fix" -- "$cur") )
            return 0 ;;
        compact)
            COMPREPLY=( $(compgen -W "--full" -- "$cur") )
            return 0 ;;
        top)
            COMPREPLY=( $(compgen -W "--once --no-clear --interval" -- "$cur") )
            return 0 ;;
        healthcheck)
            COMPREPLY=( $(compgen -W "--json" -- "$cur") )
            return 0 ;;
        init)
            COMPREPLY=( $(compgen -W "--skip-prompts" -- "$cur") )
            return 0 ;;
        logs)
            COMPREPLY=( $(compgen -W "--follow --emit --path --lines" -- "$cur") )
            return 0 ;;
    esac

    return 0
}
complete -F _tm tm
`, subcmds)
}

func zshCompletionScript() string {
	var subcmds strings.Builder
	for _, cmd := range completionSubcommands {
		fmt.Fprintf(&subcmds, "\n        '%s:%s'", cmd, completionSubcommandDescription(cmd))
	}
	return fmt.Sprintf(`#compdef tm
_tm() {
    local state
    local -a subcmds
    subcmds=(%s
    )

    _arguments -C \
        '1: :{_describe "subcommand" subcmds}' \
        '*::arg:->args'

    case $state in
        args)
            case $words[2] in
                budget) _values 'budget action' list set delete usage ;;
                webhook) _values 'webhook action' list test replay ;;
                completion) _values 'shell' bash zsh fish ;;
                cost) _values 'range' today week month 3month year all ;;
                report) _values 'report option' --weekly --monthly ;;
                export) _values 'export option' --range --format --out ;;
                analyze) _values 'analyze option' --range --json ;;
                watch) _values 'watch option' --session --types --no-color ;;
                doctor) _values 'doctor option' --json --fix ;;
                compact) _values 'compact option' --full ;;
                top) _values 'top option' --once --no-clear --interval ;;
                healthcheck) _values 'healthcheck option' --json ;;
                init) _values 'init option' --skip-prompts ;;
                logs) _values 'logs option' --follow --emit --path --lines ;;
            esac
            ;;
    esac
}
_tm "$@"
`, subcmds.String())
}

func fishCompletionScript() string {
	subcmds := strings.Join(completionSubcommands, " ")
	return fmt.Sprintf(`complete -c tm -f
complete -c tm -n '__fish_use_subcommand' -a '%s'
complete -c tm -n '__fish_seen_subcommand_from budget' -a 'list set delete usage'
complete -c tm -n '__fish_seen_subcommand_from webhook' -a 'list test replay'
complete -c tm -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c tm -n '__fish_seen_subcommand_from cost' -a 'today week month 3month year all'
complete -c tm -n '__fish_seen_subcommand_from report' -l weekly -d 'Weekly cost report'
complete -c tm -n '__fish_seen_subcommand_from report' -l monthly -d 'Monthly cost report'
complete -c tm -n '__fish_seen_subcommand_from export' -l range -d 'Export range'
complete -c tm -n '__fish_seen_subcommand_from export' -l format -d 'Export format'
complete -c tm -n '__fish_seen_subcommand_from export' -l out -d 'Output file'
complete -c tm -n '__fish_seen_subcommand_from analyze' -l range -d 'Analysis range'
complete -c tm -n '__fish_seen_subcommand_from analyze' -l json -d 'Output JSON'
complete -c tm -n '__fish_seen_subcommand_from watch' -l session -d 'Session ID prefix'
complete -c tm -n '__fish_seen_subcommand_from watch' -l types -d 'Event types'
complete -c tm -n '__fish_seen_subcommand_from watch' -l no-color -d 'Disable ANSI colors'
complete -c tm -n '__fish_seen_subcommand_from doctor' -l json -d 'Output JSON'
complete -c tm -n '__fish_seen_subcommand_from doctor' -l fix -d 'Attempt repairs'
complete -c tm -n '__fish_seen_subcommand_from compact' -l full -d 'Run VACUUM'
complete -c tm -n '__fish_seen_subcommand_from top' -l once -d 'Print one snapshot'
complete -c tm -n '__fish_seen_subcommand_from top' -l no-clear -d 'Do not clear screen'
complete -c tm -n '__fish_seen_subcommand_from top' -l interval -d 'Refresh interval'
complete -c tm -n '__fish_seen_subcommand_from healthcheck' -l json -d 'Output JSON'
complete -c tm -n '__fish_seen_subcommand_from init' -l skip-prompts -d 'Use defaults'
complete -c tm -n '__fish_seen_subcommand_from logs' -l follow -d 'Follow log output'
complete -c tm -n '__fish_seen_subcommand_from logs' -l emit -d 'Show hook emit logs'
complete -c tm -n '__fish_seen_subcommand_from logs' -l path -d 'Log file path'
complete -c tm -n '__fish_seen_subcommand_from logs' -l lines -d 'Tail line count'
`, subcmds)
}

func completionSubcommandDescription(cmd string) string {
	switch cmd {
	case "daemon":
		return "Start daemon"
	case "emit":
		return "Emit hook event"
	case "setup":
		return "Configure Claude Code hooks"
	case "init":
		return "Interactive setup wizard"
	case "budget":
		return "Manage monthly budgets"
	case "completion":
		return "Generate shell completion script"
	case "doctor":
		return "Run installation diagnostics"
	case "web":
		return "Start web dashboard"
	default:
		return titleCase(strings.ReplaceAll(cmd, "-", " "))
	}
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
