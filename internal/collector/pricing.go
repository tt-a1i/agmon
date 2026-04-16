package collector

// Pricing tables for Claude and Codex models.
//
// TIER ASSUMPTION (Codex): agmon only sees the model name and token counts
// from JSONL logs. It cannot observe OpenAI's Standard / Batch (~50% off) /
// Flex (~25% off) / Priority (premium) tiers, nor long-context (>128k)
// pricing variants on some models. Rates here assume Standard tier
// synchronous API. If you rely heavily on Batch/Flex/Priority, cross-check
// against your OpenAI billing — dashboard cost is an estimate, not a bill.
//
// All rates are per 1,000,000 tokens (USD).

import "strings"

type modelPricing struct {
	match              []string
	inputPerMillion    float64
	outputPerMillion   float64
	cacheCreatePerMill float64
	cacheReadPerMill   float64
}

var claudePricingTable = []modelPricing{
	// --- Claude 4.x generation. Specific sub-versions MUST come before
	// their generic "opus"/"sonnet"/"haiku" fallbacks because matchPricing
	// short-circuits on first match. ---

	// Opus 4.5 / 4.6 — cheaper generation (released Oct 2025+).
	{match: []string{"opus-4-5"}, inputPerMillion: 5.0, outputPerMillion: 25.0, cacheCreatePerMill: 6.25, cacheReadPerMill: 0.50},
	{match: []string{"opus-4-6"}, inputPerMillion: 5.0, outputPerMillion: 25.0, cacheCreatePerMill: 6.25, cacheReadPerMill: 0.50},

	// Opus 4 / Opus 4.1 — original 4.x generation at the expensive price tier.
	{match: []string{"opus-4-1"}, inputPerMillion: 15.0, outputPerMillion: 75.0, cacheCreatePerMill: 18.75, cacheReadPerMill: 1.50},
	{match: []string{"opus-4"}, inputPerMillion: 15.0, outputPerMillion: 75.0, cacheCreatePerMill: 18.75, cacheReadPerMill: 1.50},

	// Sonnet 4.x — stable pricing across 4.0/4.5/4.6.
	{match: []string{"sonnet-4"}, inputPerMillion: 3.0, outputPerMillion: 15.0, cacheCreatePerMill: 3.75, cacheReadPerMill: 0.30},

	// Haiku 4.5 current pricing.
	{match: []string{"haiku-4"}, inputPerMillion: 1.0, outputPerMillion: 5.0, cacheCreatePerMill: 1.25, cacheReadPerMill: 0.10},

	// --- Claude 3.x fallbacks. ---
	{match: []string{"opus"}, inputPerMillion: 15.0, outputPerMillion: 75.0, cacheCreatePerMill: 18.75, cacheReadPerMill: 1.50},
	{match: []string{"sonnet"}, inputPerMillion: 3.0, outputPerMillion: 15.0, cacheCreatePerMill: 3.75, cacheReadPerMill: 0.30},
	{match: []string{"haiku"}, inputPerMillion: 0.25, outputPerMillion: 1.25, cacheCreatePerMill: 0.30, cacheReadPerMill: 0.025},
}

// Codex pricing (Standard tier, per 1M tokens).
// Data source: OpenRouter /models (2026-04-16), which tracks OpenAI's
// published Standard tier rates. Cache read follows the family pattern
// observed in OpenRouter data:
//   - GPT-5 family: cache = 10% of input
//   - GPT-4.1: cache = 25% of input
//   - GPT-4 (GPT-4o): cache = 50% of input
//   - Pro variants: no cache discount listed; cache rate omitted (falls back
//     to input rate — conservative)
//
// Rules are ordered "most specific first" because matchPricing iterates in
// declaration order and returns the first match.
var codexPricingTable = []modelPricing{
	// --- Pro variants (premium tier, 10-24x base pricing). ---
	{match: []string{"gpt-5.4", "pro"}, inputPerMillion: 30.0, outputPerMillion: 180.0},
	{match: []string{"gpt-5.2", "pro"}, inputPerMillion: 21.0, outputPerMillion: 168.0},

	// --- GPT-5.4 family ---
	{match: []string{"gpt-5.4", "nano"}, inputPerMillion: 0.20, outputPerMillion: 1.25, cacheReadPerMill: 0.02},
	{match: []string{"gpt-5.4", "mini"}, inputPerMillion: 0.75, outputPerMillion: 4.50, cacheReadPerMill: 0.075},
	{match: []string{"gpt-5.4"}, inputPerMillion: 2.50, outputPerMillion: 15.0, cacheReadPerMill: 0.25},

	// --- GPT-5.3 family (chat and codex share the 5.2 price tier). ---
	{match: []string{"gpt-5.3", "codex"}, inputPerMillion: 1.75, outputPerMillion: 14.0, cacheReadPerMill: 0.175},
	{match: []string{"gpt-5.3"}, inputPerMillion: 1.75, outputPerMillion: 14.0, cacheReadPerMill: 0.175},

	// --- GPT-5.2 family ---
	{match: []string{"gpt-5.2", "codex"}, inputPerMillion: 1.75, outputPerMillion: 14.0, cacheReadPerMill: 0.175},
	{match: []string{"gpt-5.2"}, inputPerMillion: 1.75, outputPerMillion: 14.0, cacheReadPerMill: 0.175},

	// --- GPT-5.1 family (codex-mini MUST come before codex, codex before base). ---
	{match: []string{"gpt-5.1", "codex", "mini"}, inputPerMillion: 0.25, outputPerMillion: 2.00, cacheReadPerMill: 0.025},
	{match: []string{"gpt-5.1", "codex"}, inputPerMillion: 1.25, outputPerMillion: 10.0, cacheReadPerMill: 0.125},
	{match: []string{"gpt-5.1"}, inputPerMillion: 1.25, outputPerMillion: 10.0, cacheReadPerMill: 0.125},

	// --- GPT-5 base family ---
	{match: []string{"gpt-5-codex"}, inputPerMillion: 1.25, outputPerMillion: 10.0, cacheReadPerMill: 0.125},
	{match: []string{"gpt-5", "nano"}, inputPerMillion: 0.05, outputPerMillion: 0.40, cacheReadPerMill: 0.005},
	{match: []string{"gpt-5", "mini"}, inputPerMillion: 0.25, outputPerMillion: 2.00, cacheReadPerMill: 0.025},
	{match: []string{"gpt-5"}, inputPerMillion: 1.25, outputPerMillion: 10.0, cacheReadPerMill: 0.125},

	// --- GPT-4.1 family (mini/nano MUST come before base). ---
	{match: []string{"gpt-4.1", "nano"}, inputPerMillion: 0.10, outputPerMillion: 0.40, cacheReadPerMill: 0.025},
	{match: []string{"gpt-4.1", "mini"}, inputPerMillion: 0.40, outputPerMillion: 1.60, cacheReadPerMill: 0.10},
	{match: []string{"gpt-4.1"}, inputPerMillion: 2.00, outputPerMillion: 8.00, cacheReadPerMill: 0.50},

	// --- GPT-4o family (mini MUST come before generic gpt-4). ---
	{match: []string{"gpt-4o", "mini"}, inputPerMillion: 0.15, outputPerMillion: 0.60, cacheReadPerMill: 0.075},
	// GPT-4o base + older gpt-4 variants fall into this rule.
	{match: []string{"gpt-4"}, inputPerMillion: 2.50, outputPerMillion: 10.0, cacheReadPerMill: 1.25},
}

func matchPricing(model string, defaultPricing modelPricing, table []modelPricing) modelPricing {
	normalized := strings.ToLower(model)
	for _, pricing := range table {
		matched := true
		for _, needle := range pricing.match {
			if !strings.Contains(normalized, needle) {
				matched = false
				break
			}
		}
		if matched {
			return pricing
		}
	}
	return defaultPricing
}

func claudePricing(model string) modelPricing {
	return matchPricing(model, modelPricing{
		inputPerMillion:    3.0,
		outputPerMillion:   15.0,
		cacheCreatePerMill: 3.75,
		cacheReadPerMill:   0.30,
	}, claudePricingTable)
}

func codexPricing(model string) modelPricing {
	return matchPricing(model, modelPricing{
		inputPerMillion:  2.0,
		outputPerMillion: 8.0,
	}, codexPricingTable)
}
