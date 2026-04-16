package collector

import (
	"math"
	"testing"
)

func TestEstimateClaudeCost(t *testing.T) {
	tests := []struct {
		name       string
		input      int
		output     int
		model      string
		wantMinUSD float64
		wantMaxUSD float64
	}{
		{
			name: "sonnet small", input: 1000, output: 500,
			model: "claude-sonnet-4-6", wantMinUSD: 0.005, wantMaxUSD: 0.015,
		},
		{
			// Opus 4.6 — cheap tier: (100000*5 + 10000*25)/1e6 = 0.75
			name: "opus-4-6 cheap tier", input: 100000, output: 10000,
			model: "claude-opus-4-6", wantMinUSD: 0.70, wantMaxUSD: 0.80,
		},
		{
			// Opus 4.1 — expensive tier: (100000*15 + 10000*75)/1e6 = 2.25
			name: "opus-4-1 expensive tier", input: 100000, output: 10000,
			model: "claude-opus-4-1-20250805", wantMinUSD: 2.20, wantMaxUSD: 2.30,
		},
		{
			// Haiku 4.5 at $1/$5: (10000*1 + 5000*5)/1e6 = 0.035
			name: "haiku 4.5", input: 10000, output: 5000,
			model: "claude-haiku-4-5", wantMinUSD: 0.034, wantMaxUSD: 0.036,
		},
		{
			name: "zero tokens", input: 0, output: 0,
			model: "claude-sonnet-4-6", wantMinUSD: 0, wantMaxUSD: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := EstimateClaudeCost(tt.input, tt.output, 0, 0, tt.model)
			if cost < tt.wantMinUSD || cost > tt.wantMaxUSD {
				t.Errorf("cost = %f, want between %f and %f", cost, tt.wantMinUSD, tt.wantMaxUSD)
			}
		})
	}
}

func TestEstimateClaudeCostZero(t *testing.T) {
	cost := EstimateClaudeCost(0, 0, 0, 0, "anything")
	if math.Abs(cost) > 0.0001 {
		t.Errorf("zero tokens should yield zero cost, got %f", cost)
	}
}

func TestEstimateClaudeCostCacheTokens(t *testing.T) {
	// Cache creation tokens (sonnet: $3.75/M) should cost more than regular input ($3/M).
	costWithCache := EstimateClaudeCost(0, 0, 1_000_000, 0, "claude-sonnet-4-6")
	costWithInput := EstimateClaudeCost(1_000_000, 0, 0, 0, "claude-sonnet-4-6")
	if costWithCache <= costWithInput {
		t.Errorf("cache creation ($%.6f) should cost more than regular input ($%.6f)", costWithCache, costWithInput)
	}

	// Cache read tokens (sonnet: $0.30/M) should cost less than regular input ($3/M).
	costCacheRead := EstimateClaudeCost(0, 0, 0, 1_000_000, "claude-sonnet-4-6")
	if costCacheRead >= costWithInput {
		t.Errorf("cache read ($%.6f) should cost less than regular input ($%.6f)", costCacheRead, costWithInput)
	}
}

// TestClaudeOpusSplitPricing verifies that opus-4/opus-4-1 use the expensive
// $15/$75 tier while opus-4-5/opus-4-6 use the cheap $5/$25 tier. Regression
// guard: before this split, all opus-4* were billed at the cheap rate.
func TestClaudeOpusSplitPricing(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{"claude-opus-4-20250514", 15.0, 75.0},
		{"claude-opus-4-1-20250805", 15.0, 75.0},
		{"claude-opus-4-5-20251120", 5.0, 25.0},
		{"claude-opus-4-6-20260210", 5.0, 25.0},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := claudePricing(tt.model)
			if p.inputPerMillion != tt.wantInput || p.outputPerMillion != tt.wantOutput {
				t.Errorf("claudePricing(%q) = (%v, %v), want (%v, %v)",
					tt.model, p.inputPerMillion, p.outputPerMillion, tt.wantInput, tt.wantOutput)
			}
		})
	}
}

func TestCodexPricing(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
		wantCache  float64
	}{
		// GPT-5 base family
		{model: "gpt-5", wantInput: 1.25, wantOutput: 10.0, wantCache: 0.125},
		{model: "gpt-5-mini", wantInput: 0.25, wantOutput: 2.0, wantCache: 0.025},
		{model: "gpt-5-nano", wantInput: 0.05, wantOutput: 0.40, wantCache: 0.005},
		{model: "gpt-5-codex", wantInput: 1.25, wantOutput: 10.0, wantCache: 0.125},
		{model: "gpt-5.1", wantInput: 1.25, wantOutput: 10.0, wantCache: 0.125},
		{model: "gpt-5.1-codex", wantInput: 1.25, wantOutput: 10.0, wantCache: 0.125},
		{model: "gpt-5.1-codex-mini", wantInput: 0.25, wantOutput: 2.00, wantCache: 0.025},

		// GPT-5.2 family
		{model: "gpt-5.2", wantInput: 1.75, wantOutput: 14.0, wantCache: 0.175},
		{model: "gpt-5.2-codex", wantInput: 1.75, wantOutput: 14.0, wantCache: 0.175},

		// GPT-5.3 family
		{model: "gpt-5.3", wantInput: 1.75, wantOutput: 14.0, wantCache: 0.175},
		{model: "gpt-5.3-codex", wantInput: 1.75, wantOutput: 14.0, wantCache: 0.175},

		// GPT-5.4 family — must NOT fall through to gpt-5/-mini/-nano rules
		{model: "gpt-5.4", wantInput: 2.50, wantOutput: 15.0, wantCache: 0.25},
		{model: "gpt-5.4-mini", wantInput: 0.75, wantOutput: 4.50, wantCache: 0.075},
		{model: "gpt-5.4-nano", wantInput: 0.20, wantOutput: 1.25, wantCache: 0.02},

		// Pro variants (no cache rate → fallback to input rate)
		{model: "gpt-5.2-pro", wantInput: 21.0, wantOutput: 168.0, wantCache: 21.0},
		{model: "gpt-5.4-pro", wantInput: 30.0, wantOutput: 180.0, wantCache: 30.0},

		// GPT-4.1 family
		{model: "gpt-4.1", wantInput: 2.0, wantOutput: 8.0, wantCache: 0.50},
		{model: "gpt-4.1-mini", wantInput: 0.40, wantOutput: 1.60, wantCache: 0.10},
		{model: "gpt-4.1-nano", wantInput: 0.10, wantOutput: 0.40, wantCache: 0.025},

		// GPT-4o family
		{model: "gpt-4o", wantInput: 2.5, wantOutput: 10.0, wantCache: 1.25},
		{model: "gpt-4o-mini", wantInput: 0.15, wantOutput: 0.60, wantCache: 0.075},

		// Unknown → default fallback
		{model: "unknown", wantInput: 2.0, wantOutput: 8.0, wantCache: 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			in, out, cache := CodexPricing(tt.model)
			if in != tt.wantInput || out != tt.wantOutput || cache != tt.wantCache {
				t.Fatalf("CodexPricing(%q) = (%v, %v, %v), want (%v, %v, %v)",
					tt.model, in, out, cache, tt.wantInput, tt.wantOutput, tt.wantCache)
			}
		})
	}
}

// TestCodexPricingMatchOrder locks in the "more specific first" matching
// contract. If the table is reordered wrong, GPT-5.4 variants would leak to
// GPT-5 base rates (huge under-billing) or Pro variants would leak to non-Pro
// rates (massive under-billing).
func TestCodexPricingMatchOrder(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantInput float64
	}{
		{"gpt-5.4-mini must not leak to gpt-5-mini", "gpt-5.4-mini-20260210", 0.75},
		{"gpt-5.4-nano must not leak to gpt-5-nano", "gpt-5.4-nano-20260210", 0.20},
		{"gpt-5.4-pro must not leak to gpt-5.4", "gpt-5.4-pro-20260210", 30.0},
		{"gpt-5.2-pro must not leak to gpt-5.2", "gpt-5.2-pro-20260210", 21.0},
		{"gpt-5.2-codex must not leak to gpt-5.2", "gpt-5.2-codex-20260210", 1.75},
		{"gpt-5-codex must not match gpt-5.2-codex rule", "gpt-5-codex", 1.25},
		{"gpt-5.1-codex-mini must not leak to gpt-5.1-codex", "gpt-5.1-codex-mini-20251113", 0.25},
		{"gpt-5.1-codex must not leak to gpt-5.1 base", "gpt-5.1-codex-20251113", 1.25},
		{"gpt-4.1-mini must not leak to gpt-4.1 base", "gpt-4.1-mini-20250401", 0.40},
		{"gpt-4.1-nano must not leak to gpt-4.1 base", "gpt-4.1-nano-20250401", 0.10},
		{"gpt-4o-mini must not leak to gpt-4 base", "gpt-4o-mini-20240718", 0.15},
		{"gpt-4o base stays on gpt-4 rule", "gpt-4o-20240513", 2.50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, _, _ := CodexPricing(tt.model)
			if in != tt.wantInput {
				t.Errorf("CodexPricing(%q).input = %v, want %v", tt.model, in, tt.wantInput)
			}
		})
	}
}
