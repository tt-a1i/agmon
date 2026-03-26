package collector

// EstimateClaudeCost estimates USD cost based on model and token counts.
// Pricing as of 2025 (per million tokens).
// inputTokens = regular input only (NOT including cache tokens).
func EstimateClaudeCost(inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int, model string) float64 {
	pricing := claudePricing(model)

	return (float64(inputTokens)*pricing.inputPerMillion +
		float64(outputTokens)*pricing.outputPerMillion +
		float64(cacheCreationTokens)*pricing.cacheCreatePerMill +
		float64(cacheReadTokens)*pricing.cacheReadPerMill) / 1_000_000
}
