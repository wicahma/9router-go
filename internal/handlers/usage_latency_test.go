package handlers

import (
	"testing"
	"time"

	"9router/proxy/internal/db"
)

func TestApplyLatencyStats_AttachesToMatchingRow(t *testing.T) {
	resp := &UsageStatsResponse{
		ByModel: map[string]ModelUsageItem{
			"kimi-k3 (prov-a)": {RawModel: "kimi-k3", Provider: "prov-a", Requests: 10},
			"other (prov-b)":   {RawModel: "other", Provider: "prov-b", Requests: 3},
		},
	}

	applyLatencyStats(resp, []db.LatencyStats{
		{Model: "kimi-k3", Provider: "prov-a", Samples: 9, P50Ms: 100, P95Ms: 400, P99Ms: 900},
	}, nil)

	got := resp.ByModel["kimi-k3 (prov-a)"]
	if got.LatencySamples != 9 || got.P50Ms != 100 || got.P95Ms != 400 || got.P99Ms != 900 {
		t.Errorf("percentiles not attached: %+v", got)
	}

	// A row with no samples must keep zero values, which the JSON omits.
	if other := resp.ByModel["other (prov-b)"]; other.LatencySamples != 0 || other.P50Ms != 0 {
		t.Errorf("row without samples gained percentiles: %+v", other)
	}
}

func TestApplyLatencyStats_MatchesOnRawModelAndProvider(t *testing.T) {
	// byModel keys carry the provider inside the display string, but the
	// percentiles arrive keyed on the raw pair. Matching must use the row's own
	// fields, never the display key.
	resp := &UsageStatsResponse{
		ByModel: map[string]ModelUsageItem{
			"m1 (My Node)": {RawModel: "m1", Provider: "prov-x", Requests: 1},
		},
	}

	applyLatencyStats(resp, []db.LatencyStats{
		{Model: "m1", Provider: "prov-x", Samples: 4, P50Ms: 7, P95Ms: 8, P99Ms: 9},
	}, nil)

	if resp.ByModel["m1 (My Node)"].P50Ms != 7 {
		t.Errorf("display-name row did not match: %+v", resp.ByModel["m1 (My Node)"])
	}
}

func TestApplyLatencyStats_ResolvesNodeIdToDisplayName(t *testing.T) {
	// usageHistory.provider stores the raw node id, while a byModel row carries
	// the node's display name. Joining the two without translating the id left
	// every row blank even though the data was there.
	rowProvider := "openai-compatible-chat-92d41f2b-f12c-4e52-93ba-576d09bfcc31"
	nodeNameMap := map[string]string{rowProvider: "My Router"}

	resp := &UsageStatsResponse{
		ByModel: map[string]ModelUsageItem{
			"space-bunny-alpha (My Router)": {RawModel: "space-bunny-alpha", Provider: "My Router", Requests: 1105},
		},
	}

	applyLatencyStats(resp, []db.LatencyStats{
		{Model: "space-bunny-alpha", Provider: rowProvider, Samples: 1105, P50Ms: 1234, P95Ms: 9012, P99Ms: 30123},
	}, nodeNameMap)

	got := resp.ByModel["space-bunny-alpha (My Router)"]
	if got.LatencySamples != 1105 || got.P50Ms != 1234 || got.P95Ms != 9012 || got.P99Ms != 30123 {
		t.Errorf("node-id percentiles did not reach the display-name row: %+v", got)
	}
}

func TestApplyLatencyStats_EmptyStatsIsNoOp(t *testing.T) {
	resp := &UsageStatsResponse{
		ByModel: map[string]ModelUsageItem{"a (p)": {RawModel: "a", Provider: "p", Requests: 2}},
	}
	applyLatencyStats(resp, nil, nil)
	if got := resp.ByModel["a (p)"]; got.LatencySamples != 0 || got.P50Ms != 0 {
		t.Errorf("nil stats changed rows: %+v", got)
	}
}

func TestCutoffLatency_MapsEveryPeriod(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		period string
		want   time.Time
	}{
		{"today", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)},
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"30d", now.Add(-30 * 24 * time.Hour)},
		{"60d", now.Add(-60 * 24 * time.Hour)},
		{"all", now.Add(-365 * 24 * time.Hour)},
		// Unknown period falls back to today rather than scanning everything.
		{"garbage", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)},
		{"", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range cases {
		got := cutoffLatency(tc.period, now)
		want := tc.want.Format(time.RFC3339)
		if got != want {
			t.Errorf("cutoffLatency(%q): want %s, got %s", tc.period, want, got)
		}
	}
}

func TestCutoffLatency_TodayIsStartOfUTCDay(t *testing.T) {
	// The handler's today/24h branches compute this same cutoff inline; a drift
	// between the two would make percentiles cover a different window than the
	// row counts next to them.
	now := time.Date(2026, 9, 27, 23, 59, 59, 0, time.UTC)
	want := "2026-09-27T00:00:00Z"
	if got := cutoffLatency("today", now); got != want {
		t.Errorf("cutoffLatency(today): want %s, got %s", want, got)
	}
}
