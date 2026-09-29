package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/chat"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/translator"
	"9router/proxy/internal/usagetracker"
	json "encoding/json/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type UsageTrendItem struct {
	Timestamp        string  `json:"timestamp"`
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	AvgLatencyMs     int64   `json:"avgLatencyMs,omitempty"`
}

type ProviderUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
}

type ModelUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	LastUsed         string  `json:"lastUsed"`
	// Latency percentiles are absent, not zero, when the requests behind this
	// row predate duration capture — a zero would read as "instant".
	LatencySamples int   `json:"latencySamples,omitempty"`
	P50Ms          int64 `json:"p50Ms,omitempty"`
	P95Ms          int64 `json:"p95Ms,omitempty"`
	P99Ms          int64 `json:"p99Ms,omitempty"`
}

type AccountUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	ConnectionID     string  `json:"connectionId"`
	AccountName      string  `json:"accountName"`
	LastUsed         string  `json:"lastUsed"`
}

type ApiKeyUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	ApiKeyMasked     string  `json:"apiKeyMasked,omitempty"`
	KeyName          string  `json:"keyName"`
	ApiKeyKey        string  `json:"apiKeyKey"`
	LastUsed         string  `json:"lastUsed"`
}

type EndpointUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	Endpoint         string  `json:"endpoint"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	LastUsed         string  `json:"lastUsed"`
}

// applyLatencyStats overlays p50/p95/p99 onto the byModel rows.
//
// Percentiles are keyed on model+provider while byModel is keyed on a display
// string, and the two code paths that build that string store different raw
// values for the provider (a node display name vs the raw id). Matching each
// row on its own (rawModel, provider) pair avoids having to reconstruct the
// display string, and a row with no matching samples simply keeps no
// percentiles.
//
// usageHistory.provider holds the raw node/connection id, whereas a byModel row
// carries the node's display name, so the percentile key has to be normalised
// through the same nodeNameMap the rows were built with. Without that the two
// sides never meet and every row silently stays blank.
func applyLatencyStats(resp *UsageStatsResponse, stats []db.LatencyStats, nodeNameMap map[string]string) {
	byPair := make(map[string]db.LatencyStats, len(stats))
	for _, s := range stats {
		byPair[s.Model+"|"+displayProvider(s.Provider, nodeNameMap)] = s
	}

	for key, item := range resp.ByModel {
		s, ok := byPair[item.RawModel+"|"+item.Provider]
		if !ok {
			continue
		}
		item.LatencySamples = s.Samples
		item.P50Ms, item.P95Ms, item.P99Ms = s.P50Ms, s.P95Ms, s.P99Ms
		resp.ByModel[key] = item
	}
}

// displayProvider maps a raw provider id to the label the dashboard shows for
// it, falling back to the id itself.
func displayProvider(provider string, nodeNameMap map[string]string) string {
	if dn, ok := nodeNameMap[provider]; ok && dn != "" {
		return dn
	}
	return provider
}

// UsageStatsResponse aggregates request statistics for the dashboard.
type UsageStatsResponse struct {
	TotalRequests         int                          `json:"totalRequests"`
	TotalPromptTokens     int64                        `json:"totalPromptTokens"`
	TotalCompletionTokens int64                        `json:"totalCompletionTokens"`
	TotalCachedTokens     int64                        `json:"totalCachedTokens"`
	TotalCost             float64                      `json:"totalCost"`
	ByProvider            map[string]ProviderUsageItem `json:"byProvider"`
	ByModel               map[string]ModelUsageItem    `json:"byModel"`
	ByAccount             map[string]AccountUsageItem  `json:"byAccount"`
	ByApiKey              map[string]ApiKeyUsageItem   `json:"byApiKey"`
	ByEndpoint            map[string]EndpointUsageItem `json:"byEndpoint"`
	Trend                 []UsageTrendItem             `json:"trend"`
	ActiveRequests        []usagetracker.ActiveRequest `json:"activeRequests"`
	RecentRequests        []usagetracker.RecentRequest `json:"recentRequests"`
	ErrorProvider         string                       `json:"errorProvider"`
	Pending               usagetracker.PendingState    `json:"pending"`
}

// HandleUsageStats returns aggregated stats for the specified period ("today", "24h", "7d", "30d", "60d").
func HandleUsageStats(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		period := r.URL.Query().Get("period")
		if period == "" {
			period = "today"
		}

		tracker := usagetracker.GetTracker()
		activeState := tracker.GetActiveState(repo)

		resp := UsageStatsResponse{
			ByProvider:     make(map[string]ProviderUsageItem),
			ByModel:        make(map[string]ModelUsageItem),
			ByAccount:      make(map[string]AccountUsageItem),
			ByApiKey:       make(map[string]ApiKeyUsageItem),
			ByEndpoint:     make(map[string]EndpointUsageItem),
			ActiveRequests: activeState.ActiveRequests,
			Pending:        activeState.Pending,
			ErrorProvider:  activeState.ErrorProvider,
		}

		// Load connection mapping for friendly account names
		connMap := make(map[string]string)
		if conns, err := repo.GetProviderConnections("", true); err == nil {
			for _, c := range conns {
				name := c.ID
				if c.Name != nil && *c.Name != "" {
					name = *c.Name
				} else if c.Email != nil && *c.Email != "" {
					name = *c.Email
				}
				connMap[c.ID] = name
			}
		}

		// Load provider nodes for custom reverse-proxy display names
		nodeNameMap := make(map[string]string)
		if nodes, err := repo.GetProviderNodes(); err == nil {
			for _, n := range nodes {
				if n.ID != "" && n.Name != nil && *n.Name != "" {
					nodeNameMap[n.ID] = *n.Name
				}
			}
		}

		// Map the masked key recorded in usage rows back to the key's label.
		keyNameMap := make(map[string]string)
		if keys, err := repo.GetApiKeys(); err == nil {
			for _, k := range keys {
				if k.Key == "" {
					continue
				}
				name := chat.MaskAPIKey(k.Key)
				if k.Name != nil && *k.Name != "" {
					name = *k.Name
				}
				keyNameMap[chat.MaskAPIKey(k.Key)] = name
			}
		}

		useDailySummary := period != "today" && period != "24h"

		if useDailySummary {
			daysLimit := 7
			if period == "30d" {
				daysLimit = 30
			} else if period == "60d" {
				daysLimit = 60
			} else if period == "all" {
				daysLimit = 365
			}

			dailyRows, err := repo.GetUsageDailyRecent(daysLimit)
			if err == nil {
				for _, rowJSON := range dailyRows {
					var dayData map[string]any
					if err := json.Unmarshal([]byte(rowJSON), &dayData); err != nil {
						continue
					}

					// byProvider
					if bp, ok := dayData["byProvider"].(map[string]any); ok {
						for prov, pVal := range bp {
							if pm, ok := pVal.(map[string]any); ok {
								cur := resp.ByProvider[prov]
								cur.Requests += getMapInt(pm, "requests")
								cur.PromptTokens += getMapInt64(pm, "promptTokens")
								cur.CompletionTokens += getMapInt64(pm, "completionTokens")
								cur.CachedTokens += getMapInt64(pm, "cachedTokens")
								cur.Cost += getMapFloat(pm, "cost")
								resp.ByProvider[prov] = cur
							}
						}
					}

					// byModel
					if bm, ok := dayData["byModel"].(map[string]any); ok {
						for mk, mVal := range bm {
							if mm, ok := mVal.(map[string]any); ok {
								rawModel, _ := mm["rawModel"].(string)
								prov, _ := mm["provider"].(string)
								if rawModel == "" {
									parts := strings.Split(mk, "|")
									rawModel = parts[0]
									if len(parts) > 1 && prov == "" {
										prov = parts[1]
									}
								}
								statsKey := rawModel
								if prov != "" {
									statsKey = rawModel + " (" + prov + ")"
								}
								displayName := prov
								if dn, ok := nodeNameMap[prov]; ok && dn != "" {
									displayName = dn
								}

								cur := resp.ByModel[statsKey]
								cur.RawModel = rawModel
								cur.Provider = displayName
								cur.Requests += getMapInt(mm, "requests")
								cur.PromptTokens += getMapInt64(mm, "promptTokens")
								cur.CompletionTokens += getMapInt64(mm, "completionTokens")
								cur.CachedTokens += getMapInt64(mm, "cachedTokens")
								cur.Cost += getMapFloat(mm, "cost")
								resp.ByModel[statsKey] = cur
							}
						}
					}

					// byAccount
					if ba, ok := dayData["byAccount"].(map[string]any); ok {
						for connID, aVal := range ba {
							if am, ok := aVal.(map[string]any); ok {
								prov, _ := am["provider"].(string)
								accName := connMap[connID]
								if accName == "" {
									if len(connID) > 8 {
										accName = "Account " + connID[:8] + "..."
									} else {
										accName = "Account " + connID
									}
								}
								// usageDaily stores one byAccount entry per connection,
								// so the rollup key here is the account itself.
								accountKey := accName
								displayName := prov
								if dn, ok := nodeNameMap[prov]; ok && dn != "" {
									displayName = dn
								}

								cur := resp.ByAccount[accountKey]
								cur.Provider = displayName
								cur.ConnectionID = connID
								cur.AccountName = accName
								cur.Requests += getMapInt(am, "requests")
								cur.PromptTokens += getMapInt64(am, "promptTokens")
								cur.CompletionTokens += getMapInt64(am, "completionTokens")
								cur.CachedTokens += getMapInt64(am, "cachedTokens")
								cur.Cost += getMapFloat(am, "cost")
								resp.ByAccount[accountKey] = cur
							}
						}
					}

					// byApiKey
					if bk, ok := dayData["byApiKey"].(map[string]any); ok {
						for key, val := range bk {
							if vm, ok := val.(map[string]any); ok {
								cur := resp.ByApiKey[key]
								cur.RawModel, _ = vm["rawModel"].(string)
								cur.Provider = displayProvider(getMapString(vm, "provider"), nodeNameMap)
								cur.ApiKeyMasked, _ = vm["apiKey"].(string)
								cur.ApiKeyKey = cur.ApiKeyMasked
								if n, ok := keyNameMap[cur.ApiKeyMasked]; ok {
									cur.KeyName = n
								} else {
									cur.KeyName = cur.ApiKeyMasked
								}
								cur.Requests += getMapInt(vm, "requests")
								cur.PromptTokens += getMapInt64(vm, "promptTokens")
								cur.CompletionTokens += getMapInt64(vm, "completionTokens")
								cur.CachedTokens += getMapInt64(vm, "cachedTokens")
								cur.Cost += getMapFloat(vm, "cost")
								resp.ByApiKey[key] = cur
							}
						}
					}

					// byEndpoint
					if be, ok := dayData["byEndpoint"].(map[string]any); ok {
						for key, val := range be {
							if vm, ok := val.(map[string]any); ok {
								cur := resp.ByEndpoint[key]
								cur.Endpoint, _ = vm["endpoint"].(string)
								cur.RawModel, _ = vm["rawModel"].(string)
								cur.Provider = displayProvider(getMapString(vm, "provider"), nodeNameMap)
								cur.Requests += getMapInt(vm, "requests")
								cur.PromptTokens += getMapInt64(vm, "promptTokens")
								cur.CompletionTokens += getMapInt64(vm, "completionTokens")
								cur.CachedTokens += getMapInt64(vm, "cachedTokens")
								cur.Cost += getMapFloat(vm, "cost")
								resp.ByEndpoint[key] = cur
							}
						}
					}
				}
			}
		} else {
			// Today or 24h: query usageHistory directly
			var cutoff string
			now := time.Now().UTC()
			if period == "today" {
				startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
				cutoff = startOfDay.Format(time.RFC3339)
			} else {
				cutoff = now.Add(-24 * time.Hour).Format(time.RFC3339)
			}

			histRows, err := repo.GetUsageHistorySince(cutoff)
			if err == nil {
				for _, r := range histRows {
					promptTok := int64(r.PromptTokens)
					complTok := int64(r.CompletionTokens)
					cachedTok := int64(translator.CachedTokensFromJSON([]byte(r.Tokens)))
					entryCost := r.Cost

					provName := r.Provider
					provDisplayName := provName
					if dn, ok := nodeNameMap[provName]; ok && dn != "" {
						provDisplayName = dn
					}

					// byProvider
					if provName != "" {
						p := resp.ByProvider[provName]
						p.Requests++
						p.PromptTokens += promptTok
						p.CompletionTokens += complTok
						p.CachedTokens += cachedTok
						p.Cost += entryCost
						resp.ByProvider[provName] = p
					}

					// byModel
					modelKey := r.Model
					if provName != "" {
						modelKey = r.Model + " (" + provName + ")"
					}
					m := resp.ByModel[modelKey]
					m.RawModel = r.Model
					m.Provider = provDisplayName
					m.Requests++
					m.PromptTokens += promptTok
					m.CompletionTokens += complTok
					m.CachedTokens += cachedTok
					m.Cost += entryCost
					if r.Timestamp > m.LastUsed {
						m.LastUsed = r.Timestamp
					}
					resp.ByModel[modelKey] = m

					// byAccount — one row per account; a connection spans many
					// models, so keying on model would show each account N times.
					if r.ConnectionID != "" {
						accName := connMap[r.ConnectionID]
						if accName == "" {
							if len(r.ConnectionID) > 8 {
								accName = "Account " + r.ConnectionID[:8] + "..."
							} else {
								accName = "Account " + r.ConnectionID
							}
						}
						a := resp.ByAccount[accName]
						if a.ConnectionID == "" {
							a.Provider = provDisplayName
						}
						a.AccountName = accName
						a.ConnectionID = r.ConnectionID
						a.Requests++
						a.PromptTokens += promptTok
						a.CompletionTokens += complTok
						a.CachedTokens += cachedTok
						a.Cost += entryCost
						if r.Timestamp > a.LastUsed {
							a.LastUsed = r.Timestamp
						}
						resp.ByAccount[accName] = a
					}

					// byApiKey
					apiKey := r.APIKey
					if apiKey == "" {
						apiKey = "local-no-key"
					}
					akKey := apiKey + "|" + r.Model + "|" + provName
					ak := resp.ByApiKey[akKey]
					ak.RawModel, ak.Provider, ak.ApiKeyMasked, ak.ApiKeyKey = r.Model, provDisplayName, apiKey, apiKey
					if n, ok := keyNameMap[apiKey]; ok {
						ak.KeyName = n
					} else {
						ak.KeyName = apiKey
					}
					ak.Requests++
					ak.PromptTokens += promptTok
					ak.CompletionTokens += complTok
					ak.CachedTokens += cachedTok
					ak.Cost += entryCost
					if r.Timestamp > ak.LastUsed {
						ak.LastUsed = r.Timestamp
					}
					resp.ByApiKey[akKey] = ak

					// byEndpoint
					epKey := r.Endpoint + "|" + r.Model + "|" + provName
					ep := resp.ByEndpoint[epKey]
					ep.Endpoint, ep.RawModel, ep.Provider = r.Endpoint, r.Model, provDisplayName
					ep.Requests++
					ep.PromptTokens += promptTok
					ep.CompletionTokens += complTok
					ep.CachedTokens += cachedTok
					ep.Cost += entryCost
					if r.Timestamp > ep.LastUsed {
						ep.LastUsed = r.Timestamp
					}
					resp.ByEndpoint[epKey] = ep
				}
			}
		}

		// Calculate total aggregates from byProvider
		for _, p := range resp.ByProvider {
			resp.TotalRequests += p.Requests
			resp.TotalPromptTokens += p.PromptTokens
			resp.TotalCompletionTokens += p.CompletionTokens
			resp.TotalCachedTokens += p.CachedTokens
			resp.TotalCost += p.Cost
		}

		// Build recent requests list (20 deduped from usageHistory)
		if recentHistory, err := repo.GetRecentUsageHistory(60); err == nil {
			var dedupedRecent []usagetracker.RecentRequest
			seen := make(map[string]bool)
			for _, rh := range recentHistory {
				if rh.PromptTokens == 0 && rh.CompletionTokens == 0 {
					continue
				}
				min := ""
				if len(rh.Timestamp) >= 16 {
					min = rh.Timestamp[:16]
				}
				k := rh.Model + "|" + rh.Provider + "|" + strconv.Itoa(rh.PromptTokens) + "|" + strconv.Itoa(rh.CompletionTokens) + "|" + min
				if seen[k] {
					continue
				}
				seen[k] = true

				cachedTok := int(translator.CachedTokensFromJSON([]byte(rh.Tokens)))
				status := "ok"
				if rh.Status != "success" && rh.Status != "ok" && rh.Status != "" {
					status = rh.Status
				}

				dedupedRecent = append(dedupedRecent, usagetracker.RecentRequest{
					Timestamp:        rh.Timestamp,
					Model:            rh.Model,
					Provider:         rh.Provider,
					PromptTokens:     rh.PromptTokens,
					CompletionTokens: rh.CompletionTokens,
					CachedTokens:     cachedTok,
					Status:           status,
				})
				if len(dedupedRecent) >= 20 {
					break
				}
			}
			resp.RecentRequests = dedupedRecent
		}

		latencyStats, err := repo.GetLatencyStatsSince(cutoffLatency(period, time.Now().UTC()), 100)
		if err == nil {
			applyLatencyStats(&resp, latencyStats, nodeNameMap)
		}

		trendDaily := period != "today" && period != "24h"
		if trend, err := repo.GetUsageTrendSince(cutoffLatency(period, time.Now().UTC()), trendDaily); err == nil {
			resp.Trend = buildTrend(trend, period, time.Now().UTC())
		}

		handlerutil.WriteJSON(w, http.StatusOK, resp)
	}
}

// cutoffLatency returns the lower bound used for latency percentiles.
//
// Percentiles always read raw usageHistory, never the daily rollup: the rollup
// stores no durations, so sampling it would silently report zeros. That means
// the window is computed directly from the requested period instead of being
// tied to the two periods that happen to query usageHistory for other reasons.
func cutoffLatency(period string, now time.Time) string {
	switch period {
	case "7d":
		return now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	case "30d":
		return now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	case "60d":
		return now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	case "all":
		return now.Add(-365 * 24 * time.Hour).Format(time.RFC3339)
	case "24h":
		return now.Add(-24 * time.Hour).Format(time.RFC3339)
	default:
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return startOfDay.Format(time.RFC3339)
	}
}

// buildTrend densifies the query result into a fixed bucket grid so the chart
// shows every hour/day in the window. Empty buckets are carried as zeros rather
// than omitted: a missing bucket collapses the x-axis and makes a quiet stretch
// look like a traffic spike.
func buildTrend(rows []db.UsageTrend, period string, now time.Time) []UsageTrendItem {
	byStamp := make(map[string]db.UsageTrend, len(rows))
	for _, row := range rows {
		byStamp[row.Timestamp] = row
	}

	var step time.Duration
	var first time.Time
	switch period {
	case "today":
		step = time.Hour
		first = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "24h":
		step = time.Hour
		first = now.Truncate(time.Hour).Add(-23 * time.Hour)
	case "30d":
		step = 24 * time.Hour
		first = now.Truncate(24 * time.Hour).Add(-29 * 24 * time.Hour)
	case "60d":
		step = 24 * time.Hour
		first = now.Truncate(24 * time.Hour).Add(-59 * 24 * time.Hour)
	default:
		step = 24 * time.Hour
		first = now.Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour)
	}

	layout := time.RFC3339
	if step >= 24*time.Hour {
		layout = "2006-01-02T15:04:05Z"
	}

	var out []UsageTrendItem
	for ts := first; !ts.After(now); ts = ts.Add(step) {
		key := ts.Format(layout)
		item := UsageTrendItem{Timestamp: key}
		if row, ok := byStamp[key]; ok {
			item.Requests = row.Requests
			item.PromptTokens = row.PromptTokens
			item.CompletionTokens = row.CompletionTokens
			item.CachedTokens = row.CachedTokens
			item.Cost = row.Cost
			item.AvgLatencyMs = row.AvgLatencyMs
		}
		out = append(out, item)
	}
	return out
}

// HandleRequestDetails returns paged request detail objects for the Details tab.
func HandleRequestDetails(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		offset := 0
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 && parsed <= 100 {
				limit = parsed
			}
		}
		if oStr := r.URL.Query().Get("offset"); oStr != "" {
			if parsed, err := strconv.Atoi(oStr); err == nil && parsed >= 0 {
				offset = parsed
			}
		}

		rawJSONs, total, err := repo.GetRequestDetailsPaged(limit, offset)
		if err != nil {
			handlerutil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		details := make([]any, 0, len(rawJSONs))
		for _, raw := range rawJSONs {
			var item map[string]any
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				continue
			}
			if tokens, ok := item["tokens"].(map[string]any); ok {
				rawTokens, marshalErr := json.Marshal(tokens)
				if marshalErr == nil {
					tokens["cached_tokens"] = float64(translator.CachedTokensFromJSON(rawTokens))
				}
			}
			details = append(details, item)
		}

		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"details": details,
			"total":   total,
			"limit":   limit,
			"offset":  offset,
		})
	}
}

// Helper functions for map extraction
func getMapString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func getMapInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func getMapInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

func getMapFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return 0
}
