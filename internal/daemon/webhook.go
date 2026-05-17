package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

const (
	webhookEventBudgetWarn       = "budget_warn"
	webhookEventBudgetOver       = "budget_over"
	webhookEventSessionHighCost  = "session_high_cost"
	webhookEventToolFailureRate  = "tool_failure_rate"
	webhookEventDaemonStarted    = "daemon_started"
	webhookEventDaemonLostEvents = "daemon_lost_events"
	webhookEventTest             = "webhook_test"

	budgetStatusOK   = "ok"
	budgetStatusWarn = "warn"
	budgetStatusOver = "over"
)

var webhookHTTPClient = &http.Client{Timeout: 5 * time.Second}

type WebhookConfig struct {
	Endpoints []EndpointConfig `json:"endpoints"`
}

type EndpointConfig struct {
	URL        string            `json:"url"`
	Events     []string          `json:"events"`
	Format     string            `json:"format"`
	Retry      RetryPolicy       `json:"retry"`
	Thresholds WebhookThresholds `json:"thresholds"`
}

type RetryPolicy struct {
	MaxAttempts           int `json:"max_attempts"`
	InitialBackoffSeconds int `json:"initial_backoff_seconds"`
}

type WebhookThresholds struct {
	SessionHighCostUSD float64 `json:"session_high_cost_usd"`
	ToolFailureRatePct float64 `json:"tool_failure_rate_pct"`
}

type WebhookPayload struct {
	Event     string                     `json:"event"`
	Budget    *BudgetWebhookBudget       `json:"budget,omitempty"`
	Session   *SessionWebhookPayload     `json:"session,omitempty"`
	Tool      *ToolFailureWebhookPayload `json:"tool,omitempty"`
	Daemon    *DaemonWebhookPayload      `json:"daemon,omitempty"`
	Timestamp time.Time                  `json:"timestamp"`
}

type BudgetWebhookPayload struct {
	Event     string              `json:"event"`
	Budget    BudgetWebhookBudget `json:"budget"`
	Timestamp time.Time           `json:"timestamp"`
}

type BudgetWebhookBudget struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Used    float64 `json:"used"`
	Limit   float64 `json:"limit"`
	Percent float64 `json:"percent"`
	Status  string  `json:"status"`
}

type SessionWebhookPayload struct {
	SessionID string  `json:"session_id"`
	Platform  string  `json:"platform"`
	Model     string  `json:"model"`
	CostUSD   float64 `json:"cost_usd"`
	Threshold float64 `json:"threshold_usd"`
}

type ToolFailureWebhookPayload struct {
	ToolName       string  `json:"tool_name"`
	CallCount      int     `json:"call_count"`
	FailCount      int     `json:"fail_count"`
	FailureRatePct float64 `json:"failure_rate_pct"`
	ThresholdPct   float64 `json:"threshold_pct"`
}

type DaemonWebhookPayload struct {
	Status                string `json:"status"`
	DroppedShutdownEvents int64  `json:"dropped_shutdown_events,omitempty"`
}

func LoadWebhookConfig() (*WebhookConfig, error) {
	path := appdir.PathFor("webhooks.json", "webhooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg WebhookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func PostWebhook(ctx context.Context, ep EndpointConfig, event string, payload any) error {
	if !endpointWantsEvent(ep, event) {
		return nil
	}
	if strings.TrimSpace(ep.URL) == "" {
		return fmt.Errorf("webhook url is required")
	}

	normalized := normalizeWebhookPayload(event, payload)

	body, err := webhookRequestBody(ep, normalized)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := webhookHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook post returned status %s", resp.Status)
	}
	return nil
}

func webhookRequestBody(ep EndpointConfig, payload WebhookPayload) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(ep.Format)) {
	case "", "json":
		return json.Marshal(payload)
	case "slack":
		return json.Marshal(map[string]string{"text": webhookText(payload)})
	case "discord":
		return json.Marshal(map[string]string{"content": webhookText(payload)})
	default:
		return nil, fmt.Errorf("unsupported webhook format %q", ep.Format)
	}
}

func normalizeWebhookPayload(event string, payload any) WebhookPayload {
	var normalized WebhookPayload
	switch p := payload.(type) {
	case WebhookPayload:
		normalized = p
	case *WebhookPayload:
		if p != nil {
			normalized = *p
		}
	case BudgetWebhookPayload:
		normalized = WebhookPayload{Budget: &p.Budget, Timestamp: p.Timestamp}
		if p.Event != "" {
			normalized.Event = p.Event
		}
	case *BudgetWebhookPayload:
		if p != nil {
			normalized = WebhookPayload{Budget: &p.Budget, Timestamp: p.Timestamp}
			if p.Event != "" {
				normalized.Event = p.Event
			}
		}
	}
	if normalized.Event == "" {
		normalized.Event = event
	}
	if normalized.Timestamp.IsZero() {
		normalized.Timestamp = time.Now().UTC()
	}
	return normalized
}

func webhookText(payload WebhookPayload) string {
	switch {
	case payload.Budget != nil:
		return budgetWebhookText(*payload.Budget)
	case payload.Session != nil:
		return fmt.Sprintf("💸 TokenMeter: session '%s' cost $%.2f exceeded $%.2f",
			payload.Session.SessionID, payload.Session.CostUSD, payload.Session.Threshold)
	case payload.Tool != nil:
		return fmt.Sprintf("⚠️ TokenMeter: tool '%s' failure rate %.0f%% (%d/%d recent calls)",
			payload.Tool.ToolName, payload.Tool.FailureRatePct, payload.Tool.FailCount, payload.Tool.CallCount)
	case payload.Daemon != nil && payload.Event == webhookEventDaemonLostEvents:
		return fmt.Sprintf("⚠️ TokenMeter: daemon stopped after dropping %d shutdown events",
			payload.Daemon.DroppedShutdownEvents)
	case payload.Daemon != nil:
		return fmt.Sprintf("TokenMeter: daemon %s", payload.Daemon.Status)
	default:
		return fmt.Sprintf("TokenMeter: %s", payload.Event)
	}
}

func budgetWebhookText(b BudgetWebhookBudget) string {
	return fmt.Sprintf("💸 TokenMeter: budget '%s' has %s (used $%.2f / $%.2f, %.0f%%)",
		b.Name, b.Status, b.Used, b.Limit, b.Percent)
}

func endpointWantsEvent(ep EndpointConfig, event string) bool {
	if len(ep.Events) == 0 {
		return false
	}
	for _, candidate := range ep.Events {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == event {
			return true
		}
	}
	return false
}

func budgetStatus(used, limit float64) (percent float64, status string) {
	if limit > 0 {
		percent = used / limit * 100
	}
	switch {
	case percent >= 100:
		status = budgetStatusOver
	case percent >= 80:
		status = budgetStatusWarn
	default:
		status = budgetStatusOK
	}
	return percent, status
}

func budgetWebhookEventForTransition(previous, current string) string {
	if current == budgetStatusOver && (previous == budgetStatusOK || previous == budgetStatusWarn) {
		return webhookEventBudgetOver
	}
	if current == budgetStatusWarn && previous == budgetStatusOK {
		return webhookEventBudgetWarn
	}
	return ""
}

func budgetWebhookPayload(budget storage.BudgetRow, used, limit float64, percent float64, status string) WebhookPayload {
	return WebhookPayload{
		Budget: &BudgetWebhookBudget{
			ID:      budget.ID,
			Name:    budget.Name,
			Used:    used,
			Limit:   limit,
			Percent: percent,
			Status:  status,
		},
		Timestamp: time.Now().UTC(),
	}
}

func (d *Daemon) dispatchWebhookEvent(ctx context.Context, event string, payload any, endpointOverrides ...[]EndpointConfig) {
	endpoints := d.webhookEndpointsSnapshot()
	if len(endpointOverrides) > 0 {
		endpoints = endpointOverrides[0]
	}
	if len(endpoints) == 0 {
		return
	}
	normalized := normalizeWebhookPayload(event, payload)
	for _, ep := range endpoints {
		ep := ep
		if !endpointWantsEvent(ep, event) {
			continue
		}
		if d.webhookRetryRunning.Load() {
			d.enqueueWebhook(ctx, ep, event, normalized)
			continue
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("webhook %s panic: %v", event, r)
				}
			}()
			if err := PostWebhook(ctx, ep, event, normalized); err != nil {
				log.Printf("webhook %s to %s failed: %v", event, ep.URL, err)
				return
			}
			log.Printf("webhook %s to %s sent", event, ep.URL)
		}()
	}
}

func (d *Daemon) dispatchWebhookEventSync(ctx context.Context, event string, payload any) {
	endpoints := d.webhookEndpointsSnapshot()
	if len(endpoints) == 0 {
		return
	}
	normalized := normalizeWebhookPayload(event, payload)
	for _, ep := range endpoints {
		if !endpointWantsEvent(ep, event) {
			continue
		}
		d.deliverWebhookWithRetry(ctx, webhookDelivery{Endpoint: ep, Event: event, Payload: normalized}, nil)
	}
}
