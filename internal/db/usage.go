package db

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

// UsageHistoryRow represents a record from the usageHistory table.
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
			return nil, fmt.Errorf("scan usageDaily recent: %w", err)
		}
		res = append(res, data)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usageDaily recent: %w", err)
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
			return nil, fmt.Errorf("scan usageHistory since %s: %w", cutoff, err)
		}
		res = append(res, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usageHistory since %s: %w", cutoff, err)
	}
	return res, nil
}

// GetUsageHistoryWatermark returns the timestamp of the newest usageHistory row.
// It rides idx_uh_ts as a covering-index seek, so it stays O(log n) no matter
// how large the table grows — that is what makes a cached aggregate safe to
// refresh from a delta instead of re-reading the whole window.
func (r *Repo) GetUsageHistoryWatermark() (string, error) {
	var ts string
	if err := r.db.QueryRow(`SELECT COALESCE(MAX(timestamp), '') FROM usageHistory`).Scan(&ts); err != nil {
		return "", fmt.Errorf("read usageHistory watermark: %w", err)
	}
	return ts, nil
}

// GetUsageHistoryBetween returns rows in (since, until] — the delta a cached
// window aggregate has yet to fold in. The bounds are exclusive on the low end
// so a row already folded in at the watermark is never counted twice.
func (r *Repo) GetUsageHistoryBetween(since, until string) ([]UsageHistoryRow, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
		       COALESCE(apiKey, ''), COALESCE(endpoint, ''), COALESCE(promptTokens, 0),
		       COALESCE(completionTokens, 0), COALESCE(cost, 0.0), COALESCE(status, 'ok'), COALESCE(tokens, '{}')
		FROM usageHistory
		WHERE timestamp > ? AND timestamp <= ?
		ORDER BY rowid ASC
	`, since, until)
	if err != nil {
		return nil, fmt.Errorf("query usageHistory between %s and %s: %w", since, until, err)
	}
	defer rows.Close()

	var res []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(&row.Timestamp, &row.Provider, &row.Model, &row.ConnectionID,
			&row.APIKey, &row.Endpoint, &row.PromptTokens, &row.CompletionTokens,
			&row.Cost, &row.Status, &row.Tokens); err != nil {
			return nil, fmt.Errorf("scan usageHistory between %s and %s: %w", since, until, err)
		}
		res = append(res, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usageHistory between %s and %s: %w", since, until, err)
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
			return nil, fmt.Errorf("scan recent usageHistory: %w", err)
		}
		res = append(res, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent usageHistory: %w", err)
	}
	return res, nil
}

// RequestDetailListRow is one row of the Details table: every value the table
// renders, and nothing else.
//
// The stored payload also carries the request messages and the response body —
// up to MaxLoggedMessages × MaxMessageContentLen plus MaxResponseContentLen
// characters, which measured ~20 KB per row. A page of 20 is 400 KB that the
// table never reads: it shows timestamps, provider, model, latency and token
// counts, and only the View action wants the text. So the list query selects
// those values out of the JSON and the payload stays behind
// GetRequestDetailByID until a row is actually opened.
type RequestDetailListRow struct {
	ID           string
	Timestamp    string
	Provider     string
	Model        string
	ConnectionID string
	Status       string
	LatencyTTFT  int64
	LatencyTotal int64
	TokensJSON   string
	Account      string
	Cost         float64
	Combo        string
	Protocol     string
}

// requestDetailListSelect pulls the renderable values straight out of the
// stored JSON. The key paths mirror what internal/handlers/chat writes into
// `latency` and `tokens`; json_extract returns NULL for a path a given row does
// not carry, which COALESCE folds to the zero the table already renders for
// "not reported".
const requestDetailListSelect = `
	SELECT id, timestamp, COALESCE(provider, ''), COALESCE(model, ''), COALESCE(connectionId, ''),
	       COALESCE(status, ''),
	       COALESCE(json_extract(data, '$.latency.ttft'), 0),
	       COALESCE(json_extract(data, '$.latency.total'), 0),
	       COALESCE(json_extract(data, '$.tokens'), '{}'),
	       COALESCE(json_extract(data, '$.account'), ''),
	       COALESCE(json_extract(data, '$.cost'), 0.0),
	       COALESCE(json_extract(data, '$.combo'), ''),
	       COALESCE(json_extract(data, '$.protocol'), ''),
	       json_valid(data)
	FROM requestDetails
	ORDER BY timestamp DESC
	LIMIT ? OFFSET ?`

// GetRequestDetailsPaged returns one page of collapsed detail rows and the
// total number of recorded requests.
//
// The count is not allowed to fail quietly. Reporting 0 next to a page that
// does carry rows is the same "zero that means broken" class this reader's
// siblings were fixed for, and it reaches the caller looking like a coherent
// empty page.
func (r *Repo) GetRequestDetailsPaged(limit, offset int) ([]RequestDetailListRow, int, error) {
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM requestDetails`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count requestDetails: %w", err)
	}

	rows, err := r.db.Query(requestDetailListSelect, limit, offset)
	if err != nil {
		return nil, total, fmt.Errorf("query requestDetails paged: %w", err)
	}
	defer rows.Close()

	res := make([]RequestDetailListRow, 0, limit)
	for rows.Next() {
		var row RequestDetailListRow
		var dataValid int
		if err := rows.Scan(&row.ID, &row.Timestamp, &row.Provider, &row.Model,
			&row.ConnectionID, &row.Status, &row.LatencyTTFT, &row.LatencyTotal, &row.TokensJSON,
			&row.Account, &row.Cost, &row.Combo, &row.Protocol,
			&dataValid); err != nil {
			return nil, total, fmt.Errorf("scan requestDetails: %w", err)
		}
		// A stored `data` that is NULL or not JSON is a broken read, not an
		// empty row. The raw-column query this replaced surfaced that as a scan
		// error; json_extract alone would fold it to the COALESCE defaults above
		// and render the row as a request with zero tokens and no payload, which
		// reads as real data. json_valid separates the two cases in the same pass.
		if dataValid != 1 {
			return nil, total, fmt.Errorf("requestDetails %s has an unreadable data column", row.ID)
		}
		res = append(res, row)
	}
	if err := rows.Err(); err != nil {
		return nil, total, fmt.Errorf("iterate requestDetails paged: %w", err)
	}
	return res, total, nil
}

// GetRequestDetailByID returns the full stored payload for one request, which
// is what the inspector shows. It is a primary-key lookup, so opening a row
// costs the same whether the table holds ten rows or ten million.
func (r *Repo) GetRequestDetailByID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("get request detail: empty id")
	}
	var data string
	if err := r.db.QueryRow(`SELECT data FROM requestDetails WHERE id = ?`, id).Scan(&data); err != nil {
		return "", fmt.Errorf("get request detail %s: %w", id, err)
	}
	return data, nil
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

type LatencyStats struct {
	Model    string
	Provider string
	Samples  int
	P50Ms    int
	P95Ms    int
	P99Ms    int
}

func (r *Repo) GetLatencyStatsSince(cutoff string, limit int) ([]LatencyStats, error) {
	rows, err := r.db.Query(`SELECT model, provider, meta FROM usageHistory WHERE timestamp >= ? AND meta != ''`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query usage history latency: %w", err)
	}
	defer rows.Close()

	type key struct {
		model    string
		provider string
	}
	groups := make(map[key][]int)

	for rows.Next() {
		var model, provider, meta string
		if err := rows.Scan(&model, &provider, &meta); err != nil {
			continue
		}
		if meta == "" {
			continue
		}
		lat := extractLatencyMs(meta)
		if lat > 0 {
			k := key{model: model, provider: provider}
			groups[k] = append(groups[k], lat)
		}
	}

	var res []LatencyStats
	for k, lats := range groups {
		if len(lats) == 0 {
			continue
		}
		sort.Ints(lats)
		n := len(lats)
		p50 := percentile(lats, 50)
		p95 := percentile(lats, 95)
		p99 := percentile(lats, 99)
		res = append(res, LatencyStats{
			Model:    k.model,
			Provider: k.provider,
			Samples:  n,
			P50Ms:    p50,
			P95Ms:    p95,
			P99Ms:    p99,
		})
	}
	return res, nil
}

func extractLatencyMs(meta string) int {
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return 0
	}
	if v, ok := m["latencyMs"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func percentile(sorted []int, pct int) int {
	if len(sorted) == 0 {
		return 0
	}
	f := float64(pct) * float64(len(sorted)) / 100.0
	idx := int(math.Ceil(f)) - 1
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

// ---- spliced from local main (upstream merge dropped these) ----

// (UpdateConnectionLastUsed lives in rotation.go — upstream's rotation-stamp
// implementation is authoritative here; the local copy was dropped on purpose.)

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
