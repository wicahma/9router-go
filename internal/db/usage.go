package db

import (
	"database/sql"
	"fmt"
	"time"
)

// GetUsageDaily returns the daily usage JSON data for a given date key.
func (r *Repo) GetUsageDaily(dateKey string) (string, error) {
	var data string
	err := r.db.QueryRow(`SELECT data FROM usageDaily WHERE dateKey = ?`, dateKey).Scan(&data)
	if err != nil {
		return "", fmt.Errorf("get daily usage %s: %w", dateKey, err)
	}
	return data, nil
}

// InsertUsageHistory logs a single request's token usage to the usageHistory table.
func (r *Repo) InsertUsageHistory(provider, model, connectionID, apiKey, endpoint string, promptTokens, completionTokens int, cost float64, status string, totalTokens int, meta string, tokensJSON string) error {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, connectionId, apiKey, endpoint, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		timestamp, provider, model, connectionID, apiKey, endpoint, promptTokens, completionTokens, cost, status, tokensJSON, meta,
	)
	if err != nil {
		return fmt.Errorf("insert usage history: %w", err)
	}
	return nil
}

// UpsertUsageDaily inserts or replaces a daily usage aggregation record.
// The data parameter should be a JSON string matching the 9router-go daily aggregation format.
// NOTE: INSERT OR REPLACE is an atomic full-row replace of the pre-merged JSON
// blob. Merging happens in-process (see handlers/chat/usage.go dailyUsageMu), so
// concurrent writers from MULTIPLE processes can still clobber each other. This
// is documented as single-writer unless the aggregation moves SQL-side.
func (r *Repo) UpsertUsageDaily(dateKey string, data string) error {
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO usageDaily (dateKey, data) VALUES (?, ?)`,
		dateKey, data,
	)
	if err != nil {
		return fmt.Errorf("upsert daily usage %s: %w", dateKey, err)
	}
	return nil
}

// InsertRequestDetail logs a request detail record for the Recent Requests dashboard tab.
func (r *Repo) InsertRequestDetail(id, provider, model, connectionID, status string, data string) error {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	_, err := r.db.Exec(
		`INSERT OR IGNORE INTO requestDetails (id, timestamp, provider, model, connectionId, status, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, timestamp, provider, model, connectionID, status, data,
	)
	if err != nil {
		return fmt.Errorf("insert request detail %s: %w", id, err)
	}
	return nil
}

// UpdateConnectionLastUsed updates the lastUsedAt timestamp and increments
// consecutiveUseCount for the given provider connection.
func (r *Repo) UpdateConnectionLastUsed(connectionID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE providerConnections SET lastUsedAt = ?, consecutiveUseCount = COALESCE(consecutiveUseCount, 0) + 1 WHERE id = ?`,
		now, connectionID,
	)
	if err != nil {
		return fmt.Errorf("update connection last used %s: %w", connectionID, err)
	}
	return nil
}

type UsageTrend struct {
	Timestamp        string
	Requests         int
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	Cost             float64
	AvgLatencyMs     int64
}

// GetUsageTrendSince returns UTC hour/day buckets from persisted request data.
func (r *Repo) GetUsageTrendSince(cutoff string, daily bool) ([]UsageTrend, error) {
	bucket := "substr(timestamp, 1, 13) || ':00:00Z'"
	if daily {
		bucket = "substr(timestamp, 1, 10) || 'T00:00:00Z'"
	}
	query := fmt.Sprintf(`
		SELECT %s, COUNT(*), COALESCE(SUM(promptTokens), 0),
		       COALESCE(SUM(completionTokens), 0), COALESCE(SUM(json_extract(tokens, '$.cached_tokens')), 0),
		       COALESCE(SUM(cost), 0),
		       CAST(COALESCE(AVG(CASE WHEN json_valid(meta) THEN json_extract(meta, '$.latencyMs') END), 0) AS INTEGER)
		FROM usageHistory
		WHERE timestamp >= ?
		GROUP BY 1 ORDER BY 1`, bucket)
	rows, err := r.db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query usage trend: %w", err)
	}
	defer rows.Close()
	var result []UsageTrend
	for rows.Next() {
		var item UsageTrend
		if err := rows.Scan(&item.Timestamp, &item.Requests, &item.PromptTokens,
			&item.CompletionTokens, &item.CachedTokens, &item.Cost, &item.AvgLatencyMs); err != nil {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

type UsageHistoryRow struct {
	Timestamp        string
	Provider         string
	Model            string
	ConnectionID     string
	APIKey           string
	Endpoint         string
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	Status           string
	Tokens           string
}

// GetUsageDailyRecent returns the most recent daily usage records up to limit.
func (r *Repo) GetUsageDailyRecent(limit int) ([]string, error) {
	rows, err := r.db.Query(`SELECT data FROM usageDaily ORDER BY dateKey DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent usageDaily: %w", err)
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		res = append(res, data)
	}
	return res, nil
}

// GetUsageHistorySince returns usage history records since the cutoff timestamp.
func (r *Repo) GetUsageHistorySince(cutoff string) ([]UsageHistoryRow, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
		       COALESCE(apiKey, ''), COALESCE(endpoint, ''), COALESCE(promptTokens, 0),
		       COALESCE(completionTokens, 0), COALESCE(cost, 0.0), COALESCE(status, 'ok'), COALESCE(tokens, '{}')
		FROM usageHistory
		WHERE timestamp >= ?
		ORDER BY rowid DESC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query usageHistory since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(
			&row.Timestamp, &row.Provider, &row.Model, &row.ConnectionID,
			&row.APIKey, &row.Endpoint, &row.PromptTokens, &row.CompletionTokens,
			&row.Cost, &row.Status, &row.Tokens,
		); err != nil {
			continue
		}
		res = append(res, row)
	}
	return res, nil
}

// GetRecentUsageHistory returns the latest N usage history records.
func (r *Repo) GetRecentUsageHistory(limit int) ([]UsageHistoryRow, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
		       COALESCE(apiKey, ''), COALESCE(endpoint, ''), COALESCE(promptTokens, 0),
		       COALESCE(completionTokens, 0), COALESCE(cost, 0.0), COALESCE(status, 'ok'), COALESCE(tokens, '{}')
		FROM usageHistory
		ORDER BY rowid DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent usageHistory: %w", err)
	}
	defer rows.Close()

	var res []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(
			&row.Timestamp, &row.Provider, &row.Model, &row.ConnectionID,
			&row.APIKey, &row.Endpoint, &row.PromptTokens, &row.CompletionTokens,
			&row.Cost, &row.Status, &row.Tokens,
		); err != nil {
			continue
		}
		res = append(res, row)
	}
	return res, nil
}

// LatencyStats is the latency distribution for one model+provider pair.
//
// Samples counts only requests that actually recorded a duration (the meta
// column is populated from a known wave onwards, so older rows are invisible
// here by design rather than counted as zero).
type LatencyStats struct {
	Model    string
	Provider string
	Samples  int
	P50Ms    int64
	P95Ms    int64
	P99Ms    int64
}

// latencyPercentilesQuery computes p50/p95/p99 per model+provider in SQL.
//
// The percentiles are nearest-rank on the ordered sample set, which needs no
// interpolation and is exact for the rank it reports. Doing this in SQLite
// keeps the whole meta column out of the process: only three integers per
// model+provider pair cross into Go. latency_ms comes from json_extract, which
// is NULL for rows written before durations were recorded, so those rows drop
// out instead of polluting the distribution with zeros.
//
// json_valid guards the whole query: json_extract raises on malformed input
// rather than returning NULL, so without it a single junk meta value would make
// the dashboard report nothing at all instead of ignoring that one row.
const latencyPercentilesQuery = `
WITH lat AS (
  SELECT model,
         COALESCE(provider, '') AS provider,
         CAST(json_extract(meta, '$.latencyMs') AS INTEGER) AS v
  FROM usageHistory
  WHERE timestamp >= ?
    AND json_valid(meta)
    AND json_extract(meta, '$.latencyMs') IS NOT NULL
),
ranked AS (
  SELECT model, provider, v,
         ROW_NUMBER() OVER (PARTITION BY model, provider ORDER BY v) AS rn,
         COUNT(*)     OVER (PARTITION BY model, provider)           AS cnt
  FROM lat
)
SELECT model, provider, cnt,
       MAX(CASE WHEN rn = (cnt * 50 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 95 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 99 + 99) / 100 THEN v END)
FROM ranked
GROUP BY model, provider
ORDER BY cnt DESC
LIMIT ?`

// GetLatencyStatsSince returns per-model+provider latency percentiles for
// requests at or after cutoff, ordered by sample count (busiest first).
func (r *Repo) GetLatencyStatsSince(cutoff string, limit int) ([]LatencyStats, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(latencyPercentilesQuery, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query latency stats since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []LatencyStats
	for rows.Next() {
		var s LatencyStats
		if err := rows.Scan(&s.Model, &s.Provider, &s.Samples, &s.P50Ms, &s.P95Ms, &s.P99Ms); err != nil {
			continue
		}
		res = append(res, s)
	}
	return res, nil
}

// GetRequestDetailsPaged returns paged raw json strings and total count from requestDetails.
// status filters rows exactly ("" = all rows).
func (r *Repo) GetRequestDetailsPaged(limit, offset int, status string) ([]string, int, error) {
	var total int
	var err error
	if status == "" {
		err = r.db.QueryRow(`SELECT COUNT(*) FROM requestDetails`).Scan(&total)
	} else {
		err = r.db.QueryRow(`SELECT COUNT(*) FROM requestDetails WHERE status = ?`, status).Scan(&total)
	}
	if err != nil {
		total = 0
	}

	var rows *sql.Rows
	if status == "" {
		rows, err = r.db.Query(`
		SELECT data FROM requestDetails
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	} else {
		rows, err = r.db.Query(`
		SELECT data FROM requestDetails
		WHERE status = ?
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, status, limit, offset)
	}
	if err != nil {
		return nil, total, fmt.Errorf("query requestDetails paged: %w", err)
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			continue
		}
		res = append(res, d)
	}
	return res, total, nil
}

func (r *Repo) PruneRequestDetailsBefore(cutoff string) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM requestDetails WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune request details before %s: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune request details before %s rows affected: %w", cutoff, err)
	}
	return n, nil
}

func (r *Repo) PruneRequestDetailsKeepNewest(limit int) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM requestDetails WHERE id NOT IN (SELECT id FROM requestDetails ORDER BY timestamp DESC LIMIT ?)`, limit)
	if err != nil {
		return 0, fmt.Errorf("prune request details keep newest %d: %w", limit, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune request details keep newest %d rows affected: %w", limit, err)
	}
	return n, nil
}

func (r *Repo) PruneUsageHistoryBefore(cutoff string) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM usageHistory WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune usage history before %s: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune usage history before %s rows affected: %w", cutoff, err)
	}
	return n, nil
}
