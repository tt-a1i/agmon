package collector

import "strings"

// EstimateClaudeCost estimates USD cost based on model and token counts.
// Pricing as of 2025 (per million tokens).
func EstimateClaudeCost(inputTokens, outputTokens int, model string) float64 {
	inputPricePerM := 3.0
	outputPricePerM := 15.0

	switch {
	case strings.Contains(model, "opus"):
		inputPricePerM = 15.0
		outputPricePerM = 75.0
	case strings.Contains(model, "sonnet"):
		inputPricePerM = 3.0
		outputPricePerM = 15.0
	case strings.Contains(model, "haiku"):
		inputPricePerM = 0.25
		outputPricePerM = 1.25
	}

	return (float64(inputTokens)*inputPricePerM + float64(outputTokens)*outputPricePerM) / 1_000_000
}
