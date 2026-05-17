package daemon

import (
	"log"

	"github.com/tt-a1i/tokenmeter/internal/collector"
)

// ReloadConfig refreshes runtime configuration without restarting the daemon.
// Malformed webhook config leaves the previously loaded webhook endpoints in place.
func (d *Daemon) ReloadConfig() {
	collector.LoadPricingOverrides()

	cfg, err := LoadWebhookConfig()
	if err != nil {
		log.Printf("reload webhooks: %v", err)
		return
	}
	d.setWebhookConfig(cfg)
	log.Printf("config reloaded: %d webhook endpoints", endpointsCount(cfg))
}

func endpointsCount(cfg *WebhookConfig) int {
	if cfg == nil {
		return 0
	}
	return len(cfg.Endpoints)
}

func (d *Daemon) setWebhookConfig(cfg *WebhookConfig) {
	d.mu.Lock()
	d.webhooks = cfg
	d.mu.Unlock()
}

func (d *Daemon) webhookConfig() *WebhookConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.webhooks
}

func (d *Daemon) webhookEndpointsSnapshot() []EndpointConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.webhooks == nil || len(d.webhooks.Endpoints) == 0 {
		return nil
	}
	return append([]EndpointConfig(nil), d.webhooks.Endpoints...)
}
