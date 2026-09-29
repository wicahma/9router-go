package handlers

import (
	"9router/proxy/internal/db"
	"sort"
	"time"
)

// TtftModelItem is one model+provider row of the TTFT percentile table.
type TtftModelItem struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Samples  int    `json:"samples"`
	P5Ms     int64  `json:"p5Ms"`
	P50Ms    int64  `json:"p50Ms"`
	P95Ms    int64  `json:"p95Ms"`
}

// TtftStatsItem is the time-to-first-token side of the dashboard.
//
// FirstSample is the oldest sample in the window and not the window's own
// start. ttftMs is only recorded from a known wave onwards, so a 30d window
// reports percentiles over a handful of days; without the floor on screen the
// percentiles read as if they covered the whole period. Samples is carried for
// the same reason: it is the only honest measure of how much data is behind
// these numbers.
type TtftStatsItem struct {
	Samples     int             `json:"samples"`
	FirstSample string          `json:"firstSample,omitempty"`
	P5Ms        int64           `json:"p5Ms,omitempty"`
	P50Ms       int64           `json:"p50Ms,omitempty"`
	P95Ms       int64           `json:"p95Ms,omitempty"`
	ByModel     []TtftModelItem `json:"byModel"`
}

// CacheModelItem is the prompt-cache hit ratio for one model.
//
// HitRatio is cachedTokens over promptTokens. The ratio is derived from the
// same byModel rows the breakdown table already renders rather than from a new
// query: the dashboard already knows both numbers per model, so a second scan
// of usageHistory would only restate them.
type CacheModelItem struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	PromptTokens int64   `json:"promptTokens"`
	CachedTokens int64   `json:"cachedTokens"`
	HitRatio     float64 `json:"hitRatio"`
}

// CacheStatsItem is the prompt-cache side of the dashboard.
//
// Only the read side is visible: usageHistory.tokens carries cached_tokens, and
// the write-side fields (cache_creation_input_tokens, cache_read_input_tokens)
// are absent or zero in every persisted row, so there is nothing to report
// about cache writes. A zero PromptTokens model is excluded rather than shown
// at 0%: no prompt tokens means no ratio, not a total miss.
type CacheStatsItem struct {
	PromptTokens int64            `json:"promptTokens"`
	CachedTokens int64            `json:"cachedTokens"`
	HitRatio     float64          `json:"hitRatio"`
	ByModel      []CacheModelItem `json:"byModel"`
}

// buildTtftStats assembles the TTFT payload for a period.
func buildTtftStats(repo *db.Repo, period string, nodeNameMap map[string]string) TtftStatsItem {
	out := TtftStatsItem{ByModel: []TtftModelItem{}}

	cutoff := cutoffLatency(period, time.Now().UTC())
	overview, err := repo.GetTtftOverviewSince(cutoff)
	if err == nil {
		out.Samples = overview.Samples
		out.FirstSample = overview.FirstSample
		out.P5Ms, out.P50Ms, out.P95Ms = overview.P5Ms, overview.P50Ms, overview.P95Ms
	}

	rows, err := repo.GetTtftStatsSince(cutoff, 50)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out.ByModel = append(out.ByModel, TtftModelItem{
			Model:    row.Model,
			Provider: displayProvider(row.Provider, nodeNameMap),
			Samples:  row.Samples,
			P5Ms:     row.P5Ms,
			P50Ms:    row.P50Ms,
			P95Ms:    row.P95Ms,
		})
	}
	return out
}

// buildCacheStats derives the prompt-cache hit ratio from the byModel rows.
//
// Both counters already live on ModelUsageItem, and byModel is populated
// whichever way the period was served (raw usageHistory for today/24h, the
// usageDaily rollup beyond that), so the ratio is computed here instead of in
// another query.
func buildCacheStats(byModel map[string]ModelUsageItem) CacheStatsItem {
	out := CacheStatsItem{ByModel: []CacheModelItem{}}

	for key, item := range byModel {
		if item.PromptTokens <= 0 {
			continue
		}
		out.PromptTokens += item.PromptTokens
		out.CachedTokens += item.CachedTokens
		ratio := float64(item.CachedTokens) / float64(item.PromptTokens)
		name := item.RawModel
		if name == "" {
			name = key
		}
		out.ByModel = append(out.ByModel, CacheModelItem{
			Model:        name,
			Provider:     item.Provider,
			PromptTokens: item.PromptTokens,
			CachedTokens: item.CachedTokens,
			HitRatio:     ratio,
		})
	}

	if out.PromptTokens > 0 {
		out.HitRatio = float64(out.CachedTokens) / float64(out.PromptTokens)
	}

	// Busiest first: a model with a handful of prompt tokens can post a
	// spectacular ratio that means nothing at this scale.
	sort.Slice(out.ByModel, func(i, j int) bool {
		return out.ByModel[i].PromptTokens > out.ByModel[j].PromptTokens
	})
	return out
}
