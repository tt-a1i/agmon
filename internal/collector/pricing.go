package collector

// Pricing tables for Claude and Codex models.
//
// TIER ASSUMPTION (Codex): tokenmeter only sees the model name and token counts
// from JSONL logs. It cannot observe OpenAI's Standard / Batch (~50% off) /
// Flex (~25% off) / Priority (premium) tiers, nor long-context (>128k)
// pricing variants on some models. Rates here assume Standard tier short
// context synchronous API. If you rely heavily on Batch/Flex/Priority/long
// context/regional processing, cross-check against your provider billing —
// dashboard cost is an estimate, not a bill.
//
// Pricing overrides: on package startup, LoadPricingOverrides reads the optional
// app data pricing.json file and prepends valid user rules to the built-in
// Claude/Codex tables. Invalid or malformed override files are logged and
// ignored so hardcoded pricing remains the fallback.
//
// All rates are per 1,000,000 tokens (USD).

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/tt-a1i/tokenmeter/internal/appdir"
)

type modelPricing struct {
	match              []string
	inputPerMillion    float64
	outputPerMillion   float64
	cacheCreatePerMill float64
	cacheReadPerMill   float64
}

var defaultClaudePricingTable = []modelPricing{
	// --- Claude 4.x generation. Specific sub-versions MUST come before
	// their generic "opus"/"sonnet"/"haiku" fallbacks because matchPricing
	// short-circuits on first match. ---

	// Opus 4.5+ — current cheaper generation.
	{match: []string{"opus-4-7"}, inputPerMillion: 5.0, outputPerMillion: 25.0, cacheCreatePerMill: 6.25, cacheReadPerMill: 0.50},
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

var claudePricingTable = clonePricingTable(defaultClaudePricingTable)

// Codex/OpenAI pricing (Standard short-context tier, per 1M tokens).
// Data source: OpenAI API pricing page. Cache read follows the listed
// "cached input" rate when available. Pro variants do not list cached input,
// so their cache rate is omitted and falls back to input rate conservatively.
//
// Rules are ordered "most specific first" because matchPricing iterates in
// declaration order and returns the first match.
var defaultCodexPricingTable = []modelPricing{
	// --- Pro variants (premium tier, 10-24x base pricing). ---
	{match: []string{"gpt-5.5", "pro"}, inputPerMillion: 30.0, outputPerMillion: 180.0},
	{match: []string{"gpt-5.4", "pro"}, inputPerMillion: 30.0, outputPerMillion: 180.0},
	{match: []string{"gpt-5.2", "pro"}, inputPerMillion: 21.0, outputPerMillion: 168.0},
	{match: []string{"gpt-5-pro"}, inputPerMillion: 15.0, outputPerMillion: 120.0},

	// --- GPT-5.5 family ---
	{match: []string{"gpt-5.5"}, inputPerMillion: 5.0, outputPerMillion: 30.0, cacheReadPerMill: 0.50},

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

var codexPricingTable = clonePricingTable(defaultCodexPricingTable)

type pricingOverridesFile struct {
	Claude []pricingOverrideRule `json:"claude"`
	Codex  []pricingOverrideRule `json:"codex"`
}

type pricingOverrideRule struct {
	Match              []string `json:"match"`
	InputPerMillion    float64  `json:"inputPerMillion"`
	OutputPerMillion   float64  `json:"outputPerMillion"`
	CacheCreatePerMill float64  `json:"cacheCreatePerMill"`
	CacheReadPerMill   float64  `json:"cacheReadPerMill"`
}

func init() {
	LoadPricingOverrides()
}

// LoadPricingOverrides resets pricing tables to the hardcoded defaults, then
// prepends valid entries from the optional app data pricing.json file.
func LoadPricingOverrides() {
	claudePricingTable = clonePricingTable(defaultClaudePricingTable)
	codexPricingTable = clonePricingTable(defaultCodexPricingTable)

	path := appdir.PathFor("pricing.json", "pricing.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("pricing override: read %s: %v", path, err)
		}
		return
	}

	var overrides pricingOverridesFile
	if err := json.Unmarshal(data, &overrides); err != nil {
		log.Printf("pricing override: parse %s: %v", path, err)
		return
	}

	claudePricingTable = prependPricingOverrides("claude", path, overrides.Claude, defaultClaudePricingTable)
	codexPricingTable = prependPricingOverrides("codex", path, overrides.Codex, defaultCodexPricingTable)
}

func prependPricingOverrides(platform, path string, overrides []pricingOverrideRule, defaults []modelPricing) []modelPricing {
	table := clonePricingTable(defaults)
	valid := make([]modelPricing, 0, len(overrides))
	for i, override := range overrides {
		pricing, ok := override.modelPricing(platform, path, i)
		if ok {
			valid = append(valid, pricing)
		}
	}
	if len(valid) == 0 {
		return table
	}
	return append(valid, table...)
}

func (r pricingOverrideRule) modelPricing(platform, path string, index int) (modelPricing, bool) {
	match := make([]string, 0, len(r.Match))
	for _, needle := range r.Match {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" {
			match = append(match, needle)
		}
	}
	if len(match) == 0 {
		log.Printf("pricing override: skip %s[%d] in %s: match is required", platform, index, path)
		return modelPricing{}, false
	}
	if r.InputPerMillion < 0 || r.OutputPerMillion < 0 || r.CacheCreatePerMill < 0 || r.CacheReadPerMill < 0 {
		log.Printf("pricing override: skip %s[%d] in %s: rates must be non-negative", platform, index, path)
		return modelPricing{}, false
	}
	return modelPricing{
		match:              match,
		inputPerMillion:    r.InputPerMillion,
		outputPerMillion:   r.OutputPerMillion,
		cacheCreatePerMill: r.CacheCreatePerMill,
		cacheReadPerMill:   r.CacheReadPerMill,
	}, true
}

func clonePricingTable(table []modelPricing) []modelPricing {
	clone := make([]modelPricing, len(table))
	copy(clone, table)
	return clone
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
