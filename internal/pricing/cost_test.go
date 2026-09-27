package pricing

import "testing"

func TestEstimateCostWithSource_TableMatchIsRealPrice(t *testing.T) {
	cost, source := EstimateCostWithSource("claude-sonnet-4-20250514", 1_000_000, 1_000_000)
	if source != SourceTable {
		t.Errorf("source = %q, want %q", source, SourceTable)
	}
	if want := 3.0 + 15.0; cost != want {
		t.Errorf("cost = %v, want %v", cost, want)
	}
}

func TestEstimateCostWithSource_CatalogFillsUnconfiguredModels(t *testing.T) {
	withCatalog(t, func(model string) (float64, float64, bool) {
		if model == "kimi-k3" {
			return 3, 15, true
		}
		return 0, 0, false
	})

	cost, source := EstimateCostWithSource("kimi-k3", 1_000_000, 1_000_000)
	if source != SourceCatalog {
		t.Errorf("source = %q, want %q", source, SourceCatalog)
	}
	if want := 3.0 + 15.0; cost != want {
		t.Errorf("cost = %v, want the catalog rate %v", cost, want)
	}
}

func TestEstimateCostWithSource_CuratedTableBeatsCatalog(t *testing.T) {
	// A locally configured rate is a decision, so the catalog must not
	// overwrite it — claude-haiku is listed upstream at 4x the local rate
	// because the prefix spans two model generations.
	withCatalog(t, func(string) (float64, float64, bool) {
		return 1, 5, true
	})

	cost, source := EstimateCostWithSource("claude-haiku", 1_000_000, 1_000_000)
	if source != SourceTable {
		t.Fatalf("source = %q, want %q — the local table must win", source, SourceTable)
	}
	if want := 0.25 + 1.25; cost != want {
		t.Errorf("cost = %v, want the configured rate %v", cost, want)
	}
}

func TestEstimateCostWithSource_DefaultIsTheLastResort(t *testing.T) {
	withCatalog(t, func(string) (float64, float64, bool) { return 0, 0, false })

	cost, source := EstimateCostWithSource("totally-unlisted-model", 1_000_000, 1_000_000)
	if source != SourceDefault {
		t.Errorf("source = %q, want %q", source, SourceDefault)
	}
	if want := 1.0 + 3.0; cost != want {
		t.Errorf("cost = %v, want the default rate %v", cost, want)
	}
}

func TestEstimateCostWithSource_DefaultAppliesWhenNoCatalogInstalled(t *testing.T) {
	withCatalog(t, nil)

	_, source := EstimateCostWithSource("kimi-k3", 100, 100)
	if source != SourceDefault {
		t.Errorf("source = %q, want %q — a nil lookup must not panic or invent a catalog rate", source, SourceDefault)
	}
}

func TestEstimateCostWithSource_LongestPrefixWins(t *testing.T) {
	_, source := EstimateCostWithSource("deepseek-v4-flash", 100, 100)
	if source != SourceTable {
		t.Errorf("source = %q, want %q", source, SourceTable)
	}
	p := GetPricing("deepseek-v4-flash")
	if p.InputPer1M != 0.07 {
		t.Errorf("InputPer1M = %v, want 0.07", p.InputPer1M)
	}
}

func TestEstimateCost_MatchesWithSourceWrapper(t *testing.T) {
	for _, model := range []string{"gpt-4o", "claude-haiku", "unknown-model", "agnes-3.0-flash"} {
		want := EstimateCost(model, 1234, 567)
		got, _ := EstimateCostWithSource(model, 1234, 567)
		if got != want {
			t.Errorf("%s: EstimateCost = %v, EstimateCostWithSource = %v", model, want, got)
		}
	}
}

func TestEstimateCostWithSource_ZeroTokensStillReportsSource(t *testing.T) {
	withCatalog(t, func(string) (float64, float64, bool) { return 0, 0, false })

	cost, source := EstimateCostWithSource("kimi-k3", 0, 0)
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
	if source != SourceDefault {
		t.Errorf("source = %q, want %q — a zero cost must not be mistaken for a free tier", source, SourceDefault)
	}
}

// withCatalog installs a stub catalog lookup for the duration of one test.
func withCatalog(t *testing.T, fn func(string) (float64, float64, bool)) {
	t.Helper()
	saved := CatalogPriceLookup
	CatalogPriceLookup = fn
	t.Cleanup(func() { CatalogPriceLookup = saved })
}
