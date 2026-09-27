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

func TestEstimateCostWithSource_UnknownModelIsMarkedDefault(t *testing.T) {
	cost, source := EstimateCostWithSource("kimi-k3", 1_000_000, 1_000_000)
	if source != SourceDefault {
		t.Errorf("source = %q, want %q — an unpriced model must not look priced", source, SourceDefault)
	}
	if want := 1.0 + 3.0; cost != want {
		t.Errorf("cost = %v, want the default rate %v", cost, want)
	}
}

func TestEstimateCostWithSource_LongestPrefixWins(t *testing.T) {
	// "claude-haiku" and "claude-sonnet-4" share no prefix, but a future
	// "claude" entry would, so verify the winner is the longest one.
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
	cost, source := EstimateCostWithSource("kimi-k3", 0, 0)
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
	if source != SourceDefault {
		t.Errorf("source = %q, want %q — a zero cost must not be mistaken for a free tier", source, SourceDefault)
	}
}
