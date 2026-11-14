package models

import "strconv"

// perMillion converts a per-token price string (OpenRouter /models) to USD per million tokens.
func perMillion(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0
	}
	return f * 1_000_000
}

// Cost returns the USD cost of a call: the provider's reported cost when present, else
// tokens times the target prices (USD per million tokens). The bool reports which.
func Cost(u Usage, t Target) (float64, bool) {
	if u.Cost != nil {
		return *u.Cost, true
	}
	return (float64(u.In)*t.PriceIn + float64(u.Out)*t.PriceOut) / 1_000_000, false
}

// Estimate returns the most a call can cost: its input tokens plus maximum output at target prices (§8.4).
func Estimate(inTokens, maxOut int64, t Target) float64 {
	return (float64(inTokens)*t.PriceIn + float64(maxOut)*t.PriceOut) / 1_000_000
}

// EstimateTokens returns a conservative token count for text (4 characters per token).
func EstimateTokens(chars int) int64 { return int64(chars/4 + 1) }
