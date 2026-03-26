package collector

import "strings"

type modelPricing struct {
	match              []string
	inputPerMillion    float64
	outputPerMillion   float64
	cacheCreatePerMill float64
	cacheReadPerMill   float64
}

var claudePricingTable = []modelPricing{
	// Claude 4.x generation (must come before generic "opus"/"sonnet"/"haiku").
	{
		match:              []string{"opus-4"},
		inputPerMillion:    5.0,
		outputPerMillion:   25.0,
		cacheCreatePerMill: 6.25,
		cacheReadPerMill:   0.50,
	},
	{
		match:              []string{"sonnet-4"},
		inputPerMillion:    3.0,
		outputPerMillion:   15.0,
		cacheCreatePerMill: 3.75,
		cacheReadPerMill:   0.30,
	},
	{
		match:              []string{"haiku-4"},
		inputPerMillion:    0.80,
		outputPerMillion:   4.0,
		cacheCreatePerMill: 1.0,
		cacheReadPerMill:   0.08,
	},
	// Claude 3.x fallback.
	{
		match:              []string{"opus"},
		inputPerMillion:    15.0,
		outputPerMillion:   75.0,
		cacheCreatePerMill: 18.75,
		cacheReadPerMill:   1.50,
	},
	{
		match:              []string{"sonnet"},
		inputPerMillion:    3.0,
		outputPerMillion:   15.0,
		cacheCreatePerMill: 3.75,
		cacheReadPerMill:   0.30,
	},
	{
		match:              []string{"haiku"},
		inputPerMillion:    0.25,
		outputPerMillion:   1.25,
		cacheCreatePerMill: 0.30,
		cacheReadPerMill:   0.025,
	},
}

var codexPricingTable = []modelPricing{
	{
		match:            []string{"gpt-5", "mini"},
		inputPerMillion:  0.25,
		outputPerMillion: 2.0,
	},
	{
		match:            []string{"gpt-5.2"},
		inputPerMillion:  1.75,
		outputPerMillion: 14.0,
		cacheReadPerMill: 0.175,
	},
	{
		match:            []string{"gpt-5"},
		inputPerMillion:  1.25,
		outputPerMillion: 10.0,
	},
	{
		match:            []string{"gpt-4.1"},
		inputPerMillion:  2.0,
		outputPerMillion: 8.0,
	},
	{
		match:            []string{"gpt-4"},
		inputPerMillion:  2.5,
		outputPerMillion: 10.0,
	},
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
