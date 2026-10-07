package providers

import (
	"strings"
)

// ModelMetadata holds additional model information from the synced catalog
type ModelMetadata struct {
	InputCostPer1M  float64 `json:"inputCostPer1M,omitempty"`
	OutputCostPer1M float64 `json:"outputCostPer1M,omitempty"`
	CacheCostPer1M  float64 `json:"cacheCostPer1M,omitempty"`
	ContextWindow   int     `json:"contextWindow,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

// GetModelMetadata returns additional metadata for a model from the synced catalog
func GetModelMetadata(provider, model string) (*ModelMetadata, error) {
	if model == "" {
		return nil, nil
	}
	base := baseModelID(model)

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if globalCatalog == nil {
		return nil, nil
	}

	metadata := &ModelMetadata{}

	// Get pricing information (consensus price per model, not per provider)
	if price, ok := globalCatalog.Prices[base]; ok {
		metadata.InputCostPer1M = price.InputPer1M
		metadata.OutputCostPer1M = price.OutputPer1M
		// Cache cost is not directly available in the current schema, default to 0
		metadata.CacheCostPer1M = 0.0
	}

	// Get token limits (per provider)
	providerKey := strings.ToLower(provider)
	if mapped, ok := ProviderAliases[providerKey]; ok {
		providerKey = mapped
	}
	if limits, ok := globalCatalog.Providers[providerKey]; ok {
		if limit, ok := limits[base]; ok {
			metadata.ContextWindow = limit.ContextWindow
			metadata.MaxOutputTokens = limit.MaxOutput
		}
	}

	// Only return metadata if we found some useful information
	if metadata.ContextWindow == 0 && metadata.MaxOutputTokens == 0 &&
		metadata.InputCostPer1M == 0 && metadata.OutputCostPer1M == 0 {
		return nil, nil
	}

	return metadata, nil
}