package pricing

import "strings"

// ModelPricing holds the per-million-token rates for a model, in $/1M.
type ModelPricing struct {
	InputPer1M         float64
	OutputPer1M        float64
	CachedPer1M        float64
	ReasoningPer1M     float64
	CacheCreationPer1M float64
}

// TokenCounts is what the cost formula reads. PromptTokens is cache-INCLUSIVE:
// cached and cache-creation are subsets of it, so they are subtracted before the
// input rate applies and then charged at their own, cheaper, rate.
type TokenCounts struct {
	PromptTokens        int
	CompletionTokens    int
	CachedTokens        int
	CacheCreationTokens int
	ReasoningTokens     int
}

// freeModelNamespaces are model-id prefixes billed at zero. The namespace is
// checked before the canonical table, because the vendor-prefix strip in
// GetPricingForModel would otherwise turn "cline-free/deepseek-v4.1-flash" into
// a priced "deepseek-v4.1-flash". Upstream: FREE_MODEL_NAMESPACES in
// open-sse/providers/pricing.js (v0.5.91).
var (
	freeModelNamespaces = []string{"cline-free/"}
	// KnownFreeModels holds model IDs billed at zero that lack a standard
	// -free suffix. Canonical list shared with suggested-models filtering.
	KnownFreeModels     = []string{"big-pickle"}
)

// zeroPricing is what a free-namespace model costs.
var zeroPricing = ModelPricing{}

// IsFreeModel reports whether a model id sits in a namespace or is a known model billed at zero.
func IsFreeModel(model string) bool {
	lower := strings.ToLower(model)
	for _, ns := range freeModelNamespaces {
		if strings.HasPrefix(lower, ns) {
			return true
		}
	}
	for _, id := range KnownFreeModels {
		if lower == id {
			return true
		}
	}
	return false
}

// GetPricingForModel resolves the rates for a model, mirroring upstream's
// four-step chain (open-sse/providers/pricing.js getPricingForModel):
//
//  1. the provider's own table, so a gateway can price a model differently
//  2. a free namespace, which must not fall through to a paid rate
//  3. the canonical table, tried with and without the vendor prefix stripped
//  4. the ordered pattern table
//
// It reports false when nothing prices the model. Upstream records a cost of 0
// in that case rather than inventing a rate, and so does CalculateCost.
func GetPricingForModel(provider, model string) (ModelPricing, bool) {
	if model == "" {
		return ModelPricing{}, false
	}
	if byModel, ok := providerPricing[strings.ToLower(provider)]; ok {
		if p, ok := byModel[model]; ok {
			return p, true
		}
	}

	if IsFreeModel(model) {
		return zeroPricing, true
	}

	base := model
	if idx := strings.LastIndex(base, "/"); idx != -1 {
		base = base[idx+1:]
	}
	if p, ok := modelPricing[base]; ok {
		return p, true
	}
	if p, ok := modelPricing[model]; ok {
		return p, true
	}

	for _, row := range patternPricing {
		if matchPricingPattern(row.pattern, base) || matchPricingPattern(row.pattern, model) {
			return row.pricing, true
		}
	}
	return ModelPricing{}, false
}

// matchPricingPattern mirrors upstream matchPattern: `*` stands for any run of
// characters including `/`, the whole string must match, and the comparison is
// case-insensitive.
func matchPricingPattern(pattern, model string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return strings.EqualFold(parts[0], model)
	}
	rest := model
	if !strings.HasPrefix(strings.ToLower(rest), strings.ToLower(parts[0])) {
		return false
	}
	rest = rest[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(strings.ToLower(rest), strings.ToLower(parts[i]))
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(parts[i]):]
	}
	return strings.HasSuffix(strings.ToLower(rest), strings.ToLower(parts[len(parts)-1]))
}

// CalculateCost is the Go port of upstream calculateCostFromTokens. prompt_tokens
// is cache-inclusive, so cached and cache-creation are taken out of it before the
// input rate applies and then charged separately; a rate left at zero in the
// table falls back the way upstream does (cached → input, reasoning → output,
// cache_creation → input).
func CalculateCost(tokens TokenCounts, p ModelPricing) float64 {
	nonCachedInput := tokens.PromptTokens - tokens.CachedTokens - tokens.CacheCreationTokens
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}

	cost := float64(nonCachedInput) / 1_000_000 * p.InputPer1M
	if tokens.CachedTokens > 0 {
		rate := p.CachedPer1M
		if rate == 0 {
			rate = p.InputPer1M
		}
		cost += float64(tokens.CachedTokens) / 1_000_000 * rate
	}
	cost += float64(tokens.CompletionTokens) / 1_000_000 * p.OutputPer1M
	if tokens.ReasoningTokens > 0 {
		rate := p.ReasoningPer1M
		if rate == 0 {
			rate = p.OutputPer1M
		}
		cost += float64(tokens.ReasoningTokens) / 1_000_000 * rate
	}
	if tokens.CacheCreationTokens > 0 {
		rate := p.CacheCreationPer1M
		if rate == 0 {
			rate = p.InputPer1M
		}
		cost += float64(tokens.CacheCreationTokens) / 1_000_000 * rate
	}
	return cost
}

// EstimateCost resolves the rates for a model and prices a request against them.
// A model the tables do not price costs 0 — upstream records no invented rate
// either, and a fabricated one is worse than a visible gap in the Usage totals.
func EstimateCost(provider, model string, tokens TokenCounts) float64 {
	p, ok := GetPricingForModel(provider, model)
	if !ok {
		return 0
	}
	return CalculateCost(tokens, p)
}

// GetPricing returns the rates for a model (exposed for external use / testing).
func GetPricing(provider, model string) (ModelPricing, bool) {
	return GetPricingForModel(provider, model)
}

// Source indicates where pricing information came from.
type Source string

const (
	SourceTable   Source = "table"
	SourceDefault Source = "default"
	SourceProvider Source = "provider"
	// SourceCatalog means the rate came from the models.dev catalog rather than
	// a static table: it is the more current of the two, and reporting it
	// separately keeps the two sources from being confused.
	SourceCatalog Source = "catalog"
)

// ---- spliced from local main (upstream merge dropped these) ----

// CatalogPriceLookup resolves a model to its consensus upstream price. It is a
// variable so pricing can consume the providers catalog without importing that
// package, which would be a cycle. Set once at startup.
var CatalogPriceLookup func(model string) (inputPer1M, outputPer1M float64, ok bool)

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
