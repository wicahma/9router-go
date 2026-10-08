package db

import "fmt"

// ErrorBucket is one keyed count in an error breakdown (a status code, a model,
// a provider).
type ErrorBucket struct {
	Key   string
	Count int
}

// ErrorTrendPoint is the failure count for one time bucket.
type ErrorTrendPoint struct {
	Timestamp string
	Errors    int
}

// ErrorStats is the failure side of the dashboard: which requests failed, with
// what upstream status, and on what.
//
// Failures are read from requestDetails, not usageHistory: the success path
// writes usageHistory, while every failure writes a requestDetails row whose
// data document carries the upstream status code. usageHistory.status is only
// ever 'ok'/'success'.
type ErrorStats struct {
	Total      int
	ByStatus   []ErrorBucket
	ByModel    []ErrorBucket
	ByProvider []ErrorBucket
	Trend      []ErrorTrendPoint
}

// errorBaseQuery selects the failed rows of the window.
//
// The upstream status lives inside the JSON document rather than a column, so
// it has to be extracted; json_valid guards the extraction because
// json_extract raises on malformed input and one junk row would otherwise fail
// the whole query. The CAST is load-bearing: without it a status extracted as
// REAL fails the int scan and the row is silently dropped, the same way an
// uncast AVG() emptied the trend chart.
const errorBaseQuery = `
	SELECT CAST(COALESCE(json_extract(data, '$.response.status'), 0) AS INTEGER),
	       COALESCE(model, ''), COALESCE(provider, '')
	FROM requestDetails
	WHERE timestamp >= ? AND status = 'error' AND json_valid(data)`

// GetErrorStatsSince aggregates failures at or after cutoff.
//
// The breakdowns are computed in Go from a single scan rather than in four
// queries: the window is already narrowed by index and the whole result set is
// small, so one pass beats four round trips to SQLite.
func (r *Repo) GetErrorStatsSince(cutoff string) (*ErrorStats, error) {
	rows, err := r.db.Query(errorBaseQuery, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query error stats since %s: %w", cutoff, err)
	}
	defer rows.Close()

	stats := &ErrorStats{}
	statusCounts := map[string]int{}
	modelCounts := map[string]int{}
	providerCounts := map[string]int{}

	for rows.Next() {
		var status int
		var model, provider string
		if err := rows.Scan(&status, &model, &provider); err != nil {
			continue
		}
		stats.Total++
		statusCounts[fmt.Sprintf("%d", status)]++
		if model != "" {
			modelCounts[model]++
		}
		if provider != "" {
			providerCounts[provider]++
		}
	}

	stats.ByStatus = topBuckets(statusCounts, 10)
	stats.ByModel = topBuckets(modelCounts, 10)
	stats.ByProvider = topBuckets(providerCounts, 10)
	return stats, nil
}

// GetErrorTrendSince returns failure counts bucketed by UTC hour or day.
//
// Failure timestamps carry milliseconds while success rows do not, so the
// bucket is cut from the first 13 characters, which both formats share.
func (r *Repo) GetErrorTrendSince(cutoff string, daily bool) ([]ErrorTrendPoint, error) {
	bucket := "substr(timestamp, 1, 13) || ':00:00Z'"
	if daily {
		bucket = "substr(timestamp, 1, 10) || 'T00:00:00Z'"
	}
	query := fmt.Sprintf(`
		SELECT %s, COUNT(*) FROM requestDetails
		WHERE timestamp >= ? AND status = 'error'
		GROUP BY 1 ORDER BY 1`, bucket)

	rows, err := r.db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query error trend since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []ErrorTrendPoint
	for rows.Next() {
		var point ErrorTrendPoint
		if err := rows.Scan(&point.Timestamp, &point.Errors); err != nil {
			continue
		}
		res = append(res, point)
	}
	return res, nil
}

// AttemptBucket is one bar of the upstream-attempts histogram.
type AttemptBucket struct {
	Attempts int
	Requests int
}

// GetAttemptDistributionSince counts how many upstream forwards each request
// cost, over requests at or after cutoff.
//
// Only requests that recorded an attempt count are counted: the meta column
// predates attempt capture, so older rows have no value and folding them into a
// zero bucket would read as "one try".
func (r *Repo) GetAttemptDistributionSince(cutoff string) ([]AttemptBucket, error) {
	rows, err := r.db.Query(`
		SELECT CAST(json_extract(meta, '$.attempts') AS INTEGER) AS a, COUNT(*)
		FROM usageHistory
		WHERE timestamp >= ?
		  AND json_valid(meta)
		  AND json_extract(meta, '$.attempts') IS NOT NULL
		GROUP BY 1 ORDER BY 1`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query attempt distribution since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []AttemptBucket
	for rows.Next() {
		var bucket AttemptBucket
		if err := rows.Scan(&bucket.Attempts, &bucket.Requests); err != nil {
			continue
		}
		res = append(res, bucket)
	}
	return res, nil
}

// topBuckets flattens a count map into descending order, capped at limit.
// Ties are broken by key (ascending) for stable ordering.
func topBuckets(counts map[string]int, limit int) []ErrorBucket {
	out := make([]ErrorBucket, 0, len(counts))
	for key, count := range counts {
		out = append(out, ErrorBucket{Key: key, Count: count})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j].Count > out[j-1].Count {
				out[j], out[j-1] = out[j-1], out[j]
			} else if out[j].Count == out[j-1].Count && out[j].Key < out[j-1].Key {
				out[j], out[j-1] = out[j-1], out[j]
			} else {
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
