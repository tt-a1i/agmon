package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

type initOptions struct {
	skipPrompts bool
}

func runInit() error {
	opts, err := parseInitArgs(os.Args[2:])
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Welcome to TokenMeter!")
	fmt.Println()

	if err := initSetupHooks(); err != nil {
		return err
	}
	initCheckCodex()

	db := mustOpenDB()
	defer db.Close()

	if err := initBudgetStep(scanner, db, opts.skipPrompts); err != nil {
		return err
	}
	if err := initWebhookStep(scanner, opts.skipPrompts); err != nil {
		return err
	}
	if err := initPricingStep(scanner, opts.skipPrompts); err != nil {
		return err
	}
	if err := initDoctorStep(); err != nil {
		return err
	}

	fmt.Println("Setup complete! Run 'tokenmeter' to start.")
	return nil
}

func parseInitArgs(args []string) (initOptions, error) {
	var opts initOptions
	for _, arg := range args {
		switch arg {
		case "--skip-prompts":
			opts.skipPrompts = true
		default:
			return opts, fmt.Errorf("unknown init argument: %s", arg)
		}
	}
	return opts, nil
}

func initSetupHooks() error {
	fmt.Println("[1/6] Setting up Claude Code hooks...")
	settingsPath := filepath.Join(userHomeDir(), ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		fmt.Printf("   Detected: %s\n", settingsPath)
	} else if os.IsNotExist(err) {
		fmt.Printf("   Creating: %s\n", settingsPath)
	} else {
		fmt.Printf("   Settings check warning: %v\n", err)
	}
	runSetup()
	fmt.Printf("   ✓ Hooks installed (%d events)\n\n", len(tokenmeterHookNames))
	return nil
}

func initCheckCodex() {
	fmt.Println("[2/6] Checking Codex...")
	codexDir := filepath.Join(userHomeDir(), ".codex", "sessions")
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("   Not found: %s\n", codexDir)
			fmt.Println("   [skipped] Codex logs will appear here after Codex is installed")
			fmt.Println()
			return
		}
		fmt.Printf("   Warning: %s unreadable: %v\n\n", codexDir, err)
		return
	}
	files := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			files++
		}
	}
	fmt.Printf("   Detected: %s (%d existing files)\n", codexDir, files)
	fmt.Println("   ✓ Codex logs will be auto-monitored")
	fmt.Println()
}

func initBudgetStep(scanner *bufio.Scanner, db *storage.DB, skipPrompts bool) error {
	fmt.Println("[3/6] Set a monthly budget? (Optional)")
	if skipPrompts {
		fmt.Println("   [skipped]")
		fmt.Println()
		return nil
	}
	limitText, err := promptLine(scanner, "   Enter monthly limit USD (or press Enter to skip): ")
	if err != nil {
		return err
	}
	limitText = strings.TrimSpace(limitText)
	if limitText == "" {
		fmt.Println("   [skipped]")
		fmt.Println()
		return nil
	}
	limit, err := strconv.ParseFloat(limitText, 64)
	if err != nil || limit <= 0 {
		return fmt.Errorf("invalid monthly limit: %s", limitText)
	}
	platformText, err := promptLine(scanner, "   Platform (all/claude/codex, default: all): ")
	if err != nil {
		return err
	}
	platform, err := normalizeInitPlatform(platformText)
	if err != nil {
		return err
	}
	name := initBudgetName(platform)
	budgets, err := db.ListBudgets()
	if err != nil {
		return err
	}
	for _, budget := range budgets {
		if budget.Name == name {
			fmt.Printf("   ✓ Budget already exists: %q\n\n", name)
			return nil
		}
	}
	if _, err := db.InsertBudget(name, limit, platform); err != nil {
		return err
	}
	fmt.Printf("   ✓ Budget created: %q $%s\n\n", name, formatInitUSD(limit))
	return nil
}

func initWebhookStep(scanner *bufio.Scanner, skipPrompts bool) error {
	fmt.Println("[4/6] Configure webhooks for budget alerts? (Optional)")
	if skipPrompts {
		fmt.Println("   [skipped]")
		fmt.Println()
		return nil
	}
	url, err := promptLine(scanner, "   Webhook URL (or press Enter to skip): ")
	if err != nil {
		return err
	}
	url = strings.TrimSpace(url)
	if url == "" {
		fmt.Println("   [skipped]")
		fmt.Println()
		return nil
	}
	path := appdir.PathFor("webhooks.json", "webhooks.json")
	if err := upsertInitWebhook(path, url); err != nil {
		return err
	}
	fmt.Printf("   ✓ Webhook configured: %s\n\n", url)
	return nil
}

func initPricingStep(scanner *bufio.Scanner, skipPrompts bool) error {
	fmt.Println("[5/6] Self-hosted model pricing? (Optional)")
	fmt.Printf("   You can customize pricing via %s\n", appdir.PathFor("pricing.json", "pricing.json"))
	if skipPrompts {
		fmt.Println("   [skipped]")
		fmt.Println()
		return nil
	}
	answer, err := promptLine(scanner, "   Show example? [y/N]: ")
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
		fmt.Println(`   Example:
   {
     "models": {
       "self-hosted": {
         "input_per_million": 0.50,
         "output_per_million": 1.50
       }
     }
   }`)
	} else {
		fmt.Println("   [skipped]")
	}
	fmt.Println()
	return nil
}

func initDoctorStep() error {
	fmt.Println("[6/6] Running self-diagnostic...")
	prevArgs := os.Args
	os.Args = []string{"tokenmeter", "doctor"}
	defer func() { os.Args = prevArgs }()
	if err := runDoctor(); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func promptLine(scanner *bufio.Scanner, prompt string) (string, error) {
	fmt.Print(prompt)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return scanner.Text(), nil
}

func normalizeInitPlatform(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "", nil
	case "claude":
		return "claude", nil
	case "codex":
		return "codex", nil
	default:
		return "", fmt.Errorf("invalid platform %q (use all, claude, codex)", raw)
	}
}

func initBudgetName(platform string) string {
	switch platform {
	case "claude":
		return "Claude monthly"
	case "codex":
		return "Codex monthly"
	default:
		return "All monthly"
	}
}

func formatInitUSD(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func upsertInitWebhook(path, url string) error {
	cfg := daemon.WebhookConfig{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse existing webhooks: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, endpoint := range cfg.Endpoints {
		if endpoint.URL == url {
			return nil
		}
	}
	cfg.Endpoints = append(cfg.Endpoints, daemon.EndpointConfig{
		URL:    url,
		Events: []string{"budget_warn", "budget_over"},
		Format: "json",
	})
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func userHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
