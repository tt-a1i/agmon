package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/daemon"
)

func runWebhook() error {
	if maybePrintCmdHelp("webhook", os.Args[2:]) {
		return nil
	}
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: tokenmeter webhook <list|test|replay>")
	}
	switch os.Args[2] {
	case "list":
		return runWebhookList()
	case "test":
		return runWebhookTest()
	case "replay":
		return runWebhookReplay()
	default:
		return fmt.Errorf("unknown webhook command %q", os.Args[2])
	}
}

func runWebhookList() error {
	cfg, err := daemon.LoadWebhookConfig()
	if err != nil {
		return fmt.Errorf("load webhooks: %w", err)
	}
	if cfg == nil || len(cfg.Endpoints) == 0 {
		fmt.Println("No webhook endpoints configured")
		return nil
	}
	for i, ep := range cfg.Endpoints {
		format := strings.TrimSpace(ep.Format)
		if format == "" {
			format = "json"
		}
		fmt.Printf("%d. %s\n", i+1, ep.URL)
		fmt.Printf("   format: %s\n", format)
		fmt.Printf("   events: %s\n", strings.Join(ep.Events, ", "))
		if ep.Retry.MaxAttempts > 0 || ep.Retry.InitialBackoffSeconds > 0 {
			fmt.Printf("   retry: max_attempts=%d initial_backoff_seconds=%d\n", ep.Retry.MaxAttempts, ep.Retry.InitialBackoffSeconds)
		}
	}
	return nil
}

func runWebhookTest() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: tokenmeter webhook test <url>")
	}
	url := os.Args[3]
	payload := daemon.WebhookPayload{
		Event:  "webhook_test",
		Daemon: &daemon.DaemonWebhookPayload{Status: "test"},
	}
	if err := daemon.PostWebhook(context.Background(), daemon.EndpointConfig{
		URL: url, Format: "json", Events: []string{"webhook_test"},
	}, "webhook_test", payload); err != nil {
		return fmt.Errorf("send webhook test: %w", err)
	}
	fmt.Printf("Webhook test sent: %s\n", url)
	return nil
}

func runWebhookReplay() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: tokenmeter webhook replay")
	}
	path := daemon.WebhookDeadLetterPathForCLI()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No failed webhooks to replay")
			return nil
		}
		return fmt.Errorf("open failed webhooks: %w", err)
	}
	defer f.Close()

	sent := 0
	failed := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var dead daemon.WebhookDeadLetter
		if err := json.Unmarshal([]byte(line), &dead); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "skip malformed dead letter: %v\n", err)
			continue
		}
		err := daemon.PostWebhook(context.Background(), daemon.EndpointConfig{
			URL: dead.EndpointURL, Format: dead.Format, Events: []string{dead.Event},
		}, dead.Event, dead.Payload)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "replay %s to %s failed: %v\n", dead.Event, dead.EndpointURL, err)
			continue
		}
		sent++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read failed webhooks: %w", err)
	}
	fmt.Printf("Webhook replay complete: sent=%d failed=%d\n", sent, failed)
	return nil
}
