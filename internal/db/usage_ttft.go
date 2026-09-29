package db

import "fmt"

// TtftStats is the time-to-first-token distribution for one model+provider pair.
//
// Samples counts only streaming requests. A non-streaming exchange records
// ttftMs as 0 because no token is ever emitted early, so including those rows
// would drag the low percentiles to zero and describe a latency that no client
// ever saw.
type TtftStats struct {
	Model    string
	Provider string
	Samples  int
	P5Ms     int64
	P50Ms    int64
	P95Ms    int64
}

// TtftOverview is the whole-window distribution plus the window's sample floor.
//
// FirstSample is the timestamp of the oldest sample in range. ttftMs is only
// recorded from a known wave onwards, so a window wider than that wave reports
// percentiles over fewer days than it claims; carrying the earliest sample lets
// the caller say so instead of passing three days off as a week.
type TtftOverview struct {
	Samples     int
	FirstSample string
	P5Ms        int64
	P50Ms       int64
	P95Ms       int64
}

// ttftSamples is the shared sample set: streaming requests only, nearest-rank
// ranks computed in SQLite so the meta column never enters the process.
//
// json_extract returns NULL for a meta document without ttftMs, and NULL > 0
// is NULL rather than true, so rows written before durations were recorded drop
// out on their own. json_valid guards the extraction because json_extract
// raises on malformed input rather than returning NULL, and one junk row would
// otherwise empty the whole panel.
//
// The CAST is load-bearing for the same reason as in the error queries: an
// extracted value scanned straight into an int64 can fail or silently drop the
// row.
const ttftSamples = `
WITH ttft AS (
  SELECT model,
         COALESCE(provider, '') AS provider,
         timestamp,
         CAST(json_extract(meta, '$.ttftMs') AS INTEGER) AS v
  FROM usageHistory
  WHERE timestamp >= ?
    AND json_valid(meta)
    AND json_extract(meta, '$.ttftMs') > 0
)`

// ttftPercentilesQuery returns p5/p50/p95 per model+provider, busiest first.
const ttftPercentilesQuery = ttftSamples + `
, ranked AS (
  SELECT model, provider, v,
         ROW_NUMBER() OVER (PARTITION BY model, provider ORDER BY v) AS rn,
         COUNT(*)     OVER (PARTITION BY model, provider)           AS cnt
  FROM ttft
)
SELECT model, provider, cnt,
       MAX(CASE WHEN rn = (cnt *  5 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 50 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 95 + 99) / 100 THEN v END)
FROM ranked
GROUP BY model, provider
ORDER BY cnt DESC
LIMIT ?`

// ttftOverviewQuery returns the window-wide percentiles in a single row.
const ttftOverviewQuery = ttftSamples + `
, ranked AS (
  SELECT v, timestamp,
         ROW_NUMBER() OVER (ORDER BY v) AS rn,
         COUNT(*)     OVER ()           AS cnt
  FROM ttft
)
SELECT COALESCE(cnt, 0), COALESCE(MIN(timestamp), ''),
       MAX(CASE WHEN rn = (cnt *  5 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 50 + 99) / 100 THEN v END),
       MAX(CASE WHEN rn = (cnt * 95 + 99) / 100 THEN v END)
FROM ranked`

// GetTtftStatsSince returns time-to-first-token percentiles per model+provider
// for streaming requests at or after cutoff, ordered by sample count.
func (r *Repo) GetTtftStatsSince(cutoff string, limit int) ([]TtftStats, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(ttftPercentilesQuery, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query ttft stats since %s: %w", cutoff, err)
	}
	defer rows.Close()

	var res []TtftStats
	for rows.Next() {
		var s TtftStats
		if err := rows.Scan(&s.Model, &s.Provider, &s.Samples, &s.P5Ms, &s.P50Ms, &s.P95Ms); err != nil {
			continue
		}
		res = append(res, s)
	}
	return res, nil
}

// GetTtftOverviewSince returns percentiles over every streaming request in the
// window, with no model breakdown.
func (r *Repo) GetTtftOverviewSince(cutoff string) (TtftOverview, error) {
	var o TtftOverview
	row := r.db.QueryRow(ttftOverviewQuery, cutoff)
	if err := row.Scan(&o.Samples, &o.FirstSample, &o.P5Ms, &o.P50Ms, &o.P95Ms); err != nil {
		return TtftOverview{}, fmt.Errorf("query ttft overview since %s: %w", cutoff, err)
	}
	return o, nil
}
