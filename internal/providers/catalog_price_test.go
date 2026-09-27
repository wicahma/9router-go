package providers

import (
	"testing"
)

func TestConsensusPrices_MajorityWins(t *testing.T) {
	votes := map[string]map[[2]float64]int{
		"kimi-k3": {{3, 15}: 53, {4, 20}: 1, {2, 8}: 1},
	}
	got := consensusPrices(votes)["kimi-k3"]
	if got.InputPer1M != 3 || got.OutputPer1M != 15 {
		t.Fatalf("majority price not chosen: %+v", got)
	}
	if got.Agreement < 0.9 {
		t.Errorf("agreement should be ~0.96, got %v", got.Agreement)
	}
}

func TestConsensusPrices_NoMajorityLeavesModelUnpriced(t *testing.T) {
	// 3 providers split 1/1/1: no majority, so nothing is stored rather than
	// picking a side.
	votes := map[string]map[[2]float64]int{
		"contested": {{1, 2}: 1, {3, 6}: 1, {5, 10}: 1},
	}
	if got := consensusPrices(votes); len(got) != 0 {
		t.Fatalf("expected no entry for a 3-way split, got %+v", got)
	}
}

func TestConsensusPrices_ExactlyHalfIsATieAndIsRejected(t *testing.T) {
	// 2 against 2 has no majority. Accepting it would make the result depend on
	// Go's random map iteration order, so the same model could be priced two
	// different ways across restarts.
	votes := map[string]map[[2]float64]int{
		"half": {{1, 2}: 2, {9, 9}: 2},
	}
	if got := consensusPrices(votes); len(got) != 0 {
		t.Fatalf("a 2-2 tie must leave the model unpriced, got %+v", got)
	}
}

func TestConsensusPrices_TieIsStableAcrossRuns(t *testing.T) {
	votes := map[string]map[[2]float64]int{
		"tied": {{1, 2}: 2, {9, 9}: 2},
	}
	for range 50 {
		if got := consensusPrices(votes); len(got) != 0 {
			t.Fatalf("tie resolved nondeterministically: %+v", got)
		}
	}
}

func TestConsensusPrices_IgnoresZeroPricePairs(t *testing.T) {
	// (0,0) is a subscription plan with no marginal cost. It must be excluded
	// from the vote: 9 plan providers against 1 per-token provider would
	// otherwise set the rate to zero for everyone paying per token.
	votes := map[string]map[[2]float64]int{
		"plan": {{0, 0}: 9, {1, 2}: 1},
	}
	got := consensusPrices(votes)["plan"]
	if got.InputPer1M != 1 || got.OutputPer1M != 2 {
		t.Fatalf("plan providers must not set the rate, got %+v", got)
	}
	if got.Agreement != 1 {
		t.Errorf("agreement = %v, want 1 — the denominator must exclude plan votes", got.Agreement)
	}
}

func TestConsensusPrices_PlanOnlyModelStaysUnpriced(t *testing.T) {
	// If every provider is a plan, there is no per-token rate to report.
	votes := map[string]map[[2]float64]int{
		"allplan": {{0, 0}: 5},
	}
	if got := consensusPrices(votes); len(got) != 0 {
		t.Fatalf("expected no rate for a plan-only model, got %+v", got)
	}
}

func TestConsensusPrices_EmptyInput(t *testing.T) {
	if got := consensusPrices(nil); len(got) != 0 {
		t.Fatalf("expected empty map, got %+v", got)
	}
}

func TestGetCatalogPrice_UnloadedCatalogReturnsFalse(t *testing.T) {
	saved := globalCatalog
	globalCatalog = nil
	t.Cleanup(func() { globalCatalog = saved })

	if _, ok := GetCatalogPrice("kimi-k3"); ok {
		t.Error("expected miss when no catalog is loaded")
	}
	if _, ok := GetCatalogPrice(""); ok {
		t.Error("expected miss for empty model name")
	}
}

func TestGetCatalogPrice_StripsProviderPrefixAndTag(t *testing.T) {
	saved := globalCatalog
	globalCatalog = &SyncedCatalog{
		Prices: map[string]SyncedModelPrice{
			"kimi-k3": {InputPer1M: 3, OutputPer1M: 15, Agreement: 0.96},
		},
	}
	t.Cleanup(func() { globalCatalog = saved })

	for _, name := range []string{
		"kimi-k3",
		"moonshotai/kimi-k3",
		"KIMI-K3",
		"moonshotai/kimi-k3:free",
	} {
		got, ok := GetCatalogPrice(name)
		if !ok {
			t.Errorf("%s: expected hit", name)
			continue
		}
		if got.InputPer1M != 3 || got.OutputPer1M != 15 {
			t.Errorf("%s: wrong price %+v", name, got)
		}
	}
}
