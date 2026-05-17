package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

var (
	webhookRetryBackoffUnit = time.Second
	webhookDeadLetterMu     sync.Mutex
)

type webhookDelivery struct {
	Endpoint EndpointConfig
	Event    string
	Payload  WebhookPayload
}

type WebhookDeadLetter struct {
	Timestamp   time.Time      `json:"timestamp"`
	Event       string         `json:"event"`
	EndpointURL string         `json:"endpoint_url"`
	Format      string         `json:"format"`
	Attempts    int            `json:"attempts"`
	Error       string         `json:"error"`
	Payload     WebhookPayload `json:"payload"`
}

func (d *Daemon) startWebhookRetryLoop() {
	if d.webhookQueue == nil {
		d.webhookQueue = make(chan webhookDelivery, 1024)
	}
	d.webhookRetryRunning.Store(true)
	d.bgWG.Add(1)
	go func() {
		defer d.bgWG.Done()
		defer d.webhookRetryRunning.Store(false)
		d.webhookRetryLoop()
	}()
}

func (d *Daemon) webhookRetryLoop() {
	d.webhookRetryRunning.Store(true)
	for {
		select {
		case <-d.done:
			return
		case delivery := <-d.webhookQueue:
			d.bgWG.Add(1)
			go func() {
				defer d.bgWG.Done()
				d.deliverWebhookWithRetry(context.Background(), delivery, d.done)
			}()
		}
	}
}

func (d *Daemon) enqueueWebhook(ctx context.Context, ep EndpointConfig, event string, payload WebhookPayload) {
	delivery := webhookDelivery{Endpoint: ep, Event: event, Payload: payload}
	select {
	case d.webhookQueue <- delivery:
	case <-ctx.Done():
		log.Printf("webhook %s enqueue canceled for %s: %v", event, ep.URL, ctx.Err())
	case <-d.done:
		log.Printf("webhook %s dropped during shutdown for %s", event, ep.URL)
	default:
		log.Printf("webhook %s queue full for %s", event, ep.URL)
	}
}

func (d *Daemon) deliverWebhookWithRetry(ctx context.Context, delivery webhookDelivery, stop <-chan struct{}) {
	policy := normalizedRetryPolicy(delivery.Endpoint.Retry)
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		lastErr = PostWebhook(ctx, delivery.Endpoint, delivery.Event, delivery.Payload)
		if lastErr == nil {
			log.Printf("webhook %s to %s sent", delivery.Event, delivery.Endpoint.URL)
			return
		}
		if attempt >= policy.MaxAttempts {
			break
		}
		delay := webhookRetryDelay(policy, attempt)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		case <-stop:
			timer.Stop()
			return
		}
	}
	errText := ""
	if lastErr != nil {
		errText = lastErr.Error()
	}
	log.Printf("webhook %s to %s failed after %d attempts: %s", delivery.Event, delivery.Endpoint.URL, policy.MaxAttempts, errText)
	if err := appendWebhookDeadLetter(delivery.Endpoint, delivery.Event, delivery.Payload, policy.MaxAttempts, errText); err != nil {
		log.Printf("webhook dead letter append failed: %v", err)
	}
}

func normalizedRetryPolicy(policy RetryPolicy) RetryPolicy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	if policy.InitialBackoffSeconds <= 0 {
		policy.InitialBackoffSeconds = 5
	}
	return policy
}

func webhookRetryDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := time.Duration(policy.InitialBackoffSeconds) * webhookRetryBackoffUnit
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func appendWebhookDeadLetter(ep EndpointConfig, event string, payload WebhookPayload, attempts int, errText string) error {
	record := WebhookDeadLetter{
		Timestamp:   time.Now().UTC(),
		Event:       event,
		EndpointURL: ep.URL,
		Format:      ep.Format,
		Attempts:    attempts,
		Error:       errText,
		Payload:     normalizeWebhookPayload(event, payload),
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}

	path := webhookDeadLetterPath()
	webhookDeadLetterMu.Lock()
	defer webhookDeadLetterMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func webhookDeadLetterPath() string {
	return appdir.Path("webhooks-failed.log")
}

func WebhookDeadLetterPathForCLI() string {
	return webhookDeadLetterPath()
}
