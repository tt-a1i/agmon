package collector

import "strings"

// EstimateClaudeCost estimates USD cost based on model and token counts.
// Pricing as of 2025 (per million tokens).
// inputTokens = regular input only (NOT including cache tokens).
func EstimateClaudeCost(inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int, model string) float64 {
	inputPricePerM := 3.0
	outputPricePerM := 15.0
	cacheCreationPricePerM := 3.75
	cacheReadPricePerM := 0.30

	switch {
	case strings.Contains(model, "opus"):
		inputPricePerM = 15.0
		outputPricePerM = 75.0
		cacheCreationPricePerM = 18.75
		cacheReadPricePerM = 1.50
	case strings.Contains(model, "sonnet"):
		inputPricePerM = 3.0
		outputPricePerM = 15.0
		cacheCreationPricePerM = 3.75
		cacheReadPricePerM = 0.30
	case strings.Contains(model, "haiku"):
		inputPricePerM = 0.25
		outputPricePerM = 1.25
		cacheCreationPricePerM = 0.30
		cacheReadPricePerM = 0.025
	}

	return (float64(inputTokens)*inputPricePerM +
		float64(outputTokens)*outputPricePerM +
		float64(cacheCreationTokens)*cacheCreationPricePerM +
		float64(cacheReadTokens)*cacheReadPricePerM) / 1_000_000
}
