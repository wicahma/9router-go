package pricing

import "strings"

// Source records how a cost figure was arrived at, so a reader can tell a
// priced model from one that merely fell through to the default rate.
//
// A zero Cost is otherwise ambiguous: it can mean a genuinely free tier, a
// model absent from the table, or a request that reported no tokens. Before
// this existed every one of those looked identical in usageHistory.
type Source string

const (
	// SourceTable means the model matched a pricingTable entry and the cost is
	// a deliberately configured rate.
	SourceTable Source = "table"
	// SourceCatalog means the rate came from the models.dev catalog: the price
	// at least half of the providers quoting that model agree on. Real, but
	// consensus, not an exact quote for the connection actually used.
	SourceCatalog Source = "catalog"
	// SourceDefault means nothing matched and defaultPricing was applied. The
	// resulting number is a guess, not a price, and is not comparable with
	// the other two.
	SourceDefault Source = "default"
)

// CatalogPriceLookup resolves a model to its consensus upstream price. It is a
// variable so pricing can consume the providers catalog without importing that
// package, which would be a cycle. Set once at startup.
var CatalogPriceLookup func(model string) (inputPer1M, outputPer1M float64, ok bool)

// ModelPricing holds per-million-token costs for a model.
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// pricingTable maps model name prefixes to their pricing.
// Lookup uses longest-prefix matching so "claude-sonnet-4" catches
// "claude-sonnet-4.5", "claude-sonnet-4-20250514", etc.
//
// These entries win over the upstream catalog. Two of them disagree with
// models.dev consensus (claude-haiku is listed at 4x the local rate because the
// prefix spans two generations), and deepseek-v4-flash has no upstream majority
// at all, so letting the catalog overwrite them would make those worse. A
// deliberate local rate is a decision; the catalog is a fallback for the
// thousands of models nobody configured.
var pricingTable = map[string]ModelPricing{
	"claude-sonnet-4":   {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-haiku":      {InputPer1M: 0.25, OutputPer1M: 1.25},
	"deepseek-v4-flash": {InputPer1M: 0.07, OutputPer1M: 0.28},
	"gpt-4o":            {InputPer1M: 2.5, OutputPer1M: 10.0},
}

// defaultPricing is the fallback when no prefix matches.
var defaultPricing = ModelPricing{InputPer1M: 1.0, OutputPer1M: 3.0}

// EstimateCost calculates the USD cost for a request given the model name and token counts.
// It uses longest-prefix matching against the pricing table, falling back to defaultPricing.
func EstimateCost(model string, promptTokens, completionTokens int) float64 {
	c, _ := EstimateCostWithSource(model, promptTokens, completionTokens)
	return c
}

// EstimateCostWithSource returns the cost together with the reason that number
// was chosen, so callers can persist how much of their spend is priced and how
// much is invented.
func EstimateCostWithSource(model string, promptTokens, completionTokens int) (float64, Source) {
	p, source := lookupPricing(model)
	inputCost := float64(promptTokens) / 1_000_000 * p.InputPer1M
	outputCost := float64(completionTokens) / 1_000_000 * p.OutputPer1M
	return inputCost + outputCost, source
}

// lookupPricing finds the best matching pricing entry for a model name and
// reports whether the match came from the table or the fallback.
func lookupPricing(model string) (ModelPricing, Source) {
	model = strings.ToLower(model)

	if p, ok := pricingTable[model]; ok {
		return p, SourceTable
	}

	bestLen := 0
	bestPricing := defaultPricing
	for prefix, p := range pricingTable {
		if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestPricing = p
		}
	}

	if bestLen == 0 {
		return catalogOrDefault(model)
	}
	return bestPricing, SourceTable
}

// catalogOrDefault fills the gap for models nobody configured a rate for. The
// catalog is consulted first because a consensus upstream price is closer to
// truth than the flat default, and it reports SourceCatalog so the two are never
// summed together as if they meant the same thing.
func catalogOrDefault(model string) (ModelPricing, Source) {
	if CatalogPriceLookup != nil {
		if in, out, ok := CatalogPriceLookup(model); ok {
			return ModelPricing{InputPer1M: in, OutputPer1M: out}, SourceCatalog
		}
	}
	return defaultPricing, SourceDefault
}

// GetPricing returns the pricing for a model (exposed for external use / testing).
func GetPricing(model string) ModelPricing {
	p, _ := lookupPricing(model)
	return p
}
