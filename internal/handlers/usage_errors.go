package handlers

import (
	"9router/proxy/internal/db"
	"time"
)

// ErrorBucketItem is one row of an error breakdown.
type ErrorBucketItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// StatusBucketItem is one upstream-status row; the status stays a string so the
// client never has to re-derive "this is a 4xx / 5xx".
type StatusBucketItem struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// AttemptBucketItem is one bar of the attempts histogram.
type AttemptBucketItem struct {
	Attempts int `json:"attempts"`
	Requests int `json:"requests"`
}

// ErrorTrendItem is the failure count for one time bucket.
type ErrorTrendItem struct {
	Timestamp string `json:"timestamp"`
	Errors    int    `json:"errors"`
}

// ErrorStatsItem is the failure side of the dashboard.
type ErrorStatsItem struct {
	Total      int                 `json:"total"`
	ByStatus   []StatusBucketItem  `json:"byStatus"`
	ByModel    []ErrorBucketItem   `json:"byModel"`
	ByProvider []ErrorBucketItem   `json:"byProvider"`
	Trend      []ErrorTrendItem    `json:"trend"`
	Attempts   []AttemptBucketItem `json:"attempts"`
}

// buildErrorStats assembles the failure payload for a period.
//
// The provider display name is resolved here rather than in the query: the
// stored value is the raw node/connection id and the name lives in
// providerNodes, so the same nodeNameMap the rest of the dashboard uses is
// applied on the way out.
func buildErrorStats(repo *db.Repo, period string, nodeNameMap map[string]string) ErrorStatsItem {
	now := time.Now().UTC()
	cutoff := cutoffLatency(period, now)
	daily := period != "today" && period != "24h"

	out := ErrorStatsItem{
		ByStatus:   []StatusBucketItem{},
		ByModel:    []ErrorBucketItem{},
		ByProvider: []ErrorBucketItem{},
		Trend:      []ErrorTrendItem{},
		Attempts:   []AttemptBucketItem{},
	}

	if stats, err := repo.GetErrorStatsSince(cutoff); err == nil && stats != nil {
		out.Total = stats.Total
		for _, bucket := range stats.ByStatus {
			out.ByStatus = append(out.ByStatus, StatusBucketItem{Status: bucket.Key, Count: bucket.Count})
		}
		for _, bucket := range stats.ByModel {
			out.ByModel = append(out.ByModel, ErrorBucketItem{Key: bucket.Key, Count: bucket.Count})
		}
		for _, bucket := range stats.ByProvider {
			out.ByProvider = append(out.ByProvider, ErrorBucketItem{
				Key:   displayProvider(bucket.Key, nodeNameMap),
				Count: bucket.Count,
			})
		}
	}

	trend, err := repo.GetErrorTrendSince(cutoff, daily)
	if err != nil {
		return out
	}
	byStamp := make(map[string]int, len(trend))
	for _, point := range trend {
		byStamp[point.Timestamp] = point.Errors
	}
	// Densify onto the same grid the usage chart uses, so the two series can be
	// read against each other; a missing failure bucket must be a zero.
	for _, slot := range trendGrid(period, now) {
		out.Trend = append(out.Trend, ErrorTrendItem{Timestamp: slot, Errors: byStamp[slot]})
	}

	if attempts, err := repo.GetAttemptDistributionSince(cutoff); err == nil {
		for _, bucket := range attempts {
			out.Attempts = append(out.Attempts, AttemptBucketItem{
				Attempts: bucket.Attempts,
				Requests: bucket.Requests,
			})
		}
	}
	return out
}
