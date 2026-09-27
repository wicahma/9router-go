package providers

import "testing"

func TestGetCatalogPrice_FindsDeeplyNamespacedKeys(t *testing.T) {
	// Upstream model ids are not all one segment deep. A lookup that strips
	// only the first path segment leaves "fireworks/models/kimi-k3" in the
	// query, so the canonical bare name every caller actually sends misses a
	// price the catalog already holds.
	saved := globalCatalog
	globalCatalog = &SyncedCatalog{
		Prices: map[string]SyncedModelPrice{
			"models/kimi-k3": {InputPer1M: 3, OutputPer1M: 15, Agreement: 1},
		},
	}
	t.Cleanup(func() { globalCatalog = saved })

	got, ok := GetCatalogPrice("kimi-k3")
	if !ok {
		t.Fatal("bare name missed a price the catalog holds under a nested key")
	}
	if got.InputPer1M != 3 || got.OutputPer1M != 15 {
		t.Fatalf("wrong price %+v", got)
	}
}

func TestGetCatalogPrice_PrefersTheBareKeyOverAnyProviderAlias(t *testing.T) {
	// When a bare key exists it is the consensus rate for the model itself. A
	// provider-specific alias must never override it.
	saved := globalCatalog
	globalCatalog = &SyncedCatalog{
		Prices: map[string]SyncedModelPrice{
			"kimi-k3":              {InputPer1M: 3, OutputPer1M: 15, Agreement: 1},
			"models/kimi-k3":       {InputPer1M: 99, OutputPer1M: 99, Agreement: 0.5},
			"fireworks/kimi-k3":    {InputPer1M: 77, OutputPer1M: 77, Agreement: 0.5},
			"routers/kimi-k3":      {InputPer1M: 66, OutputPer1M: 66, Agreement: 0.5},
			"chat-completion/kimi": {InputPer1M: 55, OutputPer1M: 55, Agreement: 0.5},
		},
	}
	t.Cleanup(func() { globalCatalog = saved })

	got, ok := GetCatalogPrice("kimi-k3")
	if !ok {
		t.Fatal("expected the bare key to win")
	}
	if got.InputPer1M != 3 {
		t.Fatalf("a provider alias overrode the bare consensus rate: %+v", got)
	}
}

func TestGetCatalogPrice_PicksHighestAgreementWhenNoBareKeyExists(t *testing.T) {
	// Two providers quote the same model at different rates and neither is the
	// canonical name. Falling back to Go's map order would make the number
	// change between restarts, so the strongest agreement has to decide.
	saved := globalCatalog
	globalCatalog = &SyncedCatalog{
		Prices: map[string]SyncedModelPrice{
			"fireworks/kimi-k3": {InputPer1M: 4, OutputPer1M: 20, Agreement: 0.4},
			"models/kimi-k3":    {InputPer1M: 3, OutputPer1M: 15, Agreement: 0.9},
			"routers/kimi-k3":   {InputPer1M: 8, OutputPer1M: 40, Agreement: 0.6},
		},
	}
	t.Cleanup(func() { globalCatalog = saved })

	for range 50 {
		got, ok := GetCatalogPrice("kimi-k3")
		if !ok {
			t.Fatal("expected a hit from a provider alias")
		}
		if got.InputPer1M != 3 {
			t.Fatalf("agreement did not decide the fallback: %+v", got)
		}
	}
}

func TestGetCatalogPrice_LeavesUnrelatedNamespacedKeysAlone(t *testing.T) {
	// Stripping everything must not invent a hit for a model nobody priced.
	saved := globalCatalog
	globalCatalog = &SyncedCatalog{
		Prices: map[string]SyncedModelPrice{
			"models/some-other-model": {InputPer1M: 1, OutputPer1M: 2, Agreement: 1},
		},
	}
	t.Cleanup(func() { globalCatalog = saved })

	if _, ok := GetCatalogPrice("kimi-k3"); ok {
		t.Fatal("an unrelated alias must not price a different model")
	}
}
