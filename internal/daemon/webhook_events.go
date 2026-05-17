package daemon

import (
	"context"
	"log"
	"sort"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

const (
	defaultSessionHighCostUSD = 5.0
	defaultToolFailureRatePct = 20.0
)

func (d *Daemon) checkSessionHighCost(ctx context.Context, sessionID string) {
	session, found, err := d.db.GetSessionByIDPrefix(sessionID)
	if err != nil {
		log.Printf("session_high_cost lookup %s: %v", sessionID, err)
		return
	}
	if !found {
		return
	}

	endpoints := d.webhookEndpointsSnapshot()
	selected := make([]EndpointConfig, 0, len(endpoints))
	threshold := 0.0
	for _, ep := range endpoints {
		if !endpointWantsEvent(ep, webhookEventSessionHighCost) {
			continue
		}
		epThreshold := sessionHighCostThreshold(ep)
		if session.TotalCostUSD > epThreshold {
			selected = append(selected, ep)
			if threshold == 0 || epThreshold < threshold {
				threshold = epThreshold
			}
		}
	}
	if len(selected) == 0 {
		return
	}
	d.dispatchWebhookEvent(ctx, webhookEventSessionHighCost, WebhookPayload{
		Session: &SessionWebhookPayload{
			SessionID: session.SessionID,
			Platform:  session.Platform,
			Model:     session.Model,
			CostUSD:   session.TotalCostUSD,
			Threshold: threshold,
		},
	}, selected)
}

func (d *Daemon) checkToolFailureRates(ctx context.Context) error {
	endpoints := endpointsForEvent(d.webhookEndpointsSnapshot(), webhookEventToolFailureRate)
	if len(endpoints) == 0 {
		return nil
	}

	recent, err := d.recentToolCallsByName()
	if err != nil {
		return err
	}
	for toolName, calls := range recent {
		if len(calls) == 0 {
			continue
		}
		if len(calls) > 100 {
			calls = calls[:100]
		}
		failCount := 0
		for _, call := range calls {
			if isFailedToolStatus(call.Status) {
				failCount++
			}
		}
		rate := float64(failCount) / float64(len(calls)) * 100
		selected := make([]EndpointConfig, 0, len(endpoints))
		threshold := 0.0
		for _, ep := range endpoints {
			epThreshold := toolFailureRateThreshold(ep)
			if rate > epThreshold {
				selected = append(selected, ep)
				if threshold == 0 || epThreshold < threshold {
					threshold = epThreshold
				}
			}
		}
		if d.toolFailureLastAlert == nil {
			d.toolFailureLastAlert = make(map[string]float64)
		}
		if len(selected) == 0 {
			d.toolFailureLastAlert[toolName] = 0
			continue
		}
		if previous := d.toolFailureLastAlert[toolName]; previous >= threshold {
			continue
		}
		d.toolFailureLastAlert[toolName] = rate
		d.dispatchWebhookEvent(ctx, webhookEventToolFailureRate, WebhookPayload{
			Tool: &ToolFailureWebhookPayload{
				ToolName:       toolName,
				CallCount:      len(calls),
				FailCount:      failCount,
				FailureRatePct: rate,
				ThresholdPct:   threshold,
			},
		}, selected)
	}
	return nil
}

func (d *Daemon) recentToolCallsByName() (map[string][]storage.ToolCallRow, error) {
	sessions, err := d.db.ListSessionsLimit(500)
	if err != nil {
		return nil, err
	}
	byTool := make(map[string][]storage.ToolCallRow)
	for _, session := range sessions {
		calls, err := d.db.ListToolCalls(session.SessionID, 100)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			if call.ToolName == "" {
				continue
			}
			byTool[call.ToolName] = append(byTool[call.ToolName], call)
		}
	}
	for toolName, calls := range byTool {
		sort.Slice(calls, func(i, j int) bool {
			return calls[i].StartTime.After(calls[j].StartTime)
		})
		byTool[toolName] = calls
	}
	return byTool, nil
}

func endpointsForEvent(endpoints []EndpointConfig, event string) []EndpointConfig {
	selected := make([]EndpointConfig, 0, len(endpoints))
	for _, ep := range endpoints {
		if endpointWantsEvent(ep, event) {
			selected = append(selected, ep)
		}
	}
	return selected
}

func sessionHighCostThreshold(ep EndpointConfig) float64 {
	if ep.Thresholds.SessionHighCostUSD > 0 {
		return ep.Thresholds.SessionHighCostUSD
	}
	return defaultSessionHighCostUSD
}

func toolFailureRateThreshold(ep EndpointConfig) float64 {
	if ep.Thresholds.ToolFailureRatePct > 0 {
		return ep.Thresholds.ToolFailureRatePct
	}
	return defaultToolFailureRatePct
}

func isFailedToolStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "fail" || status == "error"
}
