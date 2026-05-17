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
	webhookEventBudgetWarn = "budget_warn"
	webhookEventBudgetOver = "budget_over"

	budgetStatusOK   = "ok"
	budgetStatusWarn = "warn"
	budgetStatusOver = "over"
)

var webhookHTTPClient = &http.Client{Timeout: 5 * time.Second}

type WebhookConfig struct {
	Endpoints []EndpointConfig `json:"endpoints"`
}

type EndpointConfig struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Format string   `json:"format"`
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

func PostWebhook(ctx context.Context, ep EndpointConfig, event string, payload BudgetWebhookPayload) error {
	if !endpointWantsEvent(ep, event) {
		return nil
	}
	if strings.TrimSpace(ep.URL) == "" {
		return fmt.Errorf("webhook url is required")
	}

	payload.Event = event
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}

	body, err := webhookRequestBody(ep, payload)
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

func webhookRequestBody(ep EndpointConfig, payload BudgetWebhookPayload) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(ep.Format)) {
	case "", "json":
		return json.Marshal(payload)
	case "slack":
		return json.Marshal(map[string]string{"text": budgetWebhookText(payload.Budget)})
	case "discord":
		return json.Marshal(map[string]string{"content": budgetWebhookText(payload.Budget)})
	default:
		return nil, fmt.Errorf("unsupported webhook format %q", ep.Format)
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

func budgetWebhookPayload(budget storage.BudgetRow, used, limit float64, percent float64, status string) BudgetWebhookPayload {
	return BudgetWebhookPayload{
		Budget: BudgetWebhookBudget{
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

func (d *Daemon) dispatchWebhookEvent(ctx context.Context, event string, payload BudgetWebhookPayload) {
	if d.webhooks == nil || len(d.webhooks.Endpoints) == 0 {
		return
	}
	for _, ep := range d.webhooks.Endpoints {
		ep := ep
		if !endpointWantsEvent(ep, event) {
			continue
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("webhook %s panic: %v", event, r)
				}
			}()
			if err := PostWebhook(ctx, ep, event, payload); err != nil {
				log.Printf("webhook %s to %s failed: %v", event, ep.URL, err)
				return
			}
			log.Printf("webhook %s to %s sent", event, ep.URL)
		}()
	}
}
