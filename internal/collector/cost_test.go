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
			name: "opus large", input: 100000, output: 10000,
			model: "claude-opus-4-6", wantMinUSD: 0.5, wantMaxUSD: 1.0,
		},
		{
			name: "haiku cheap", input: 10000, output: 5000,
			model: "claude-haiku-4-5", wantMinUSD: 0.02, wantMaxUSD: 0.04,
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

func TestCodexPricing(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
		wantCache  float64
	}{
		{model: "gpt-5-mini", wantInput: 0.25, wantOutput: 2.0, wantCache: 0.25},
		{model: "gpt-5.2", wantInput: 1.75, wantOutput: 14.0, wantCache: 0.175},
		{model: "gpt-5-codex", wantInput: 1.25, wantOutput: 10.0, wantCache: 1.25},
		{model: "gpt-4.1", wantInput: 2.0, wantOutput: 8.0, wantCache: 2.0},
		{model: "unknown", wantInput: 2.0, wantOutput: 8.0, wantCache: 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			in, out, cache := CodexPricing(tt.model)
			if in != tt.wantInput || out != tt.wantOutput || cache != tt.wantCache {
				t.Fatalf("CodexPricing(%q) = (%v, %v, %v), want (%v, %v, %v)", tt.model, in, out, cache, tt.wantInput, tt.wantOutput, tt.wantCache)
			}
		})
	}
}
