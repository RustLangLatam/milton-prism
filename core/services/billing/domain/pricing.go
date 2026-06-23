package domain

import "strings"

// ModelPrice is the per-million-token (MTok) USD price sheet for one model, split
// by billing tier. Anthropic bills fresh input, cache-write (cache creation),
// cache-read, and output at different rates.
type ModelPrice struct {
	InputPerMTok      float64 // fresh (non-cached) input tokens
	CacheWritePerMTok float64 // tokens written to a new cache entry (5m TTL)
	CacheReadPerMTok  float64 // tokens served from an existing cache entry
	OutputPerMTok     float64 // output tokens
}

// modelPriceTable is the approximate price sheet used to ESTIMATE the cost of a
// generation run when no real API cost is available (subscription / OAuth mode,
// where Claude Code reports total_cost_usd = 0). Rates are USD per MTok and are
// a configurable approximation, not an authoritative invoice. When the model id
// is unknown we fall back to the most expensive (opus) tier so the estimate is
// conservative (never under-counts spend).
//
// Keys are matched as case-insensitive substrings of the reported model id (the
// agent reports ids like "claude-opus-4-8[1m]" or "claude-sonnet-4-6"). Longer
// keys are tried first so "opus-4-8" wins over a hypothetical "opus".
var modelPriceTable = map[string]ModelPrice{
	"opus-4-8":  {InputPerMTok: 5, CacheWritePerMTok: 6.25, CacheReadPerMTok: 0.50, OutputPerMTok: 25},
	"opus-4-7":  {InputPerMTok: 5, CacheWritePerMTok: 6.25, CacheReadPerMTok: 0.50, OutputPerMTok: 25},
	"opus-4-6":  {InputPerMTok: 5, CacheWritePerMTok: 6.25, CacheReadPerMTok: 0.50, OutputPerMTok: 25},
	"sonnet-4-6": {InputPerMTok: 3, CacheWritePerMTok: 3.75, CacheReadPerMTok: 0.30, OutputPerMTok: 15},
	"haiku-4-5":  {InputPerMTok: 1, CacheWritePerMTok: 1.25, CacheReadPerMTok: 0.10, OutputPerMTok: 5},
	"fable-5":    {InputPerMTok: 10, CacheWritePerMTok: 12.50, CacheReadPerMTok: 1.00, OutputPerMTok: 50},
}

// fallbackModelPriceKey is the price tier used when the reported model id matches
// no known key. opus-4-8 is the most expensive tier, so the estimate stays
// conservative (it never under-counts spend for an unrecognised model).
const fallbackModelPriceKey = "opus-4-8"

// priceForModel resolves the price sheet for a model id by longest-key
// case-insensitive substring match, falling back to the opus tier.
func priceForModel(model string) ModelPrice {
	m := strings.ToLower(strings.TrimSpace(model))
	if m != "" {
		// Prefer the longest matching key so more specific ids win.
		var best string
		for key := range modelPriceTable {
			if strings.Contains(m, key) && len(key) > len(best) {
				best = key
			}
		}
		if best != "" {
			return modelPriceTable[best]
		}
	}
	return modelPriceTable[fallbackModelPriceKey]
}

// EstimateCostUSD estimates the USD cost of an LLM run from its token tiers and
// model id using the approximate price sheet. Use this ONLY when the real API
// cost is unavailable (subscription mode, total_cost_usd == 0). The four token
// counts are billed at their respective rates:
//
//	est = input/1e6*inRate + cacheCreation/1e6*cacheWrite +
//	      cacheRead/1e6*cacheRead + output/1e6*outRate
func EstimateCostUSD(model string, inputTokens, cacheCreationTokens, cacheReadTokens, outputTokens int64) float64 {
	p := priceForModel(model)
	const mtok = 1_000_000.0
	return float64(inputTokens)/mtok*p.InputPerMTok +
		float64(cacheCreationTokens)/mtok*p.CacheWritePerMTok +
		float64(cacheReadTokens)/mtok*p.CacheReadPerMTok +
		float64(outputTokens)/mtok*p.OutputPerMTok
}
