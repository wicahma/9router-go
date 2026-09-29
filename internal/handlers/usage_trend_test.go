package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/dbtest"
	"fmt"
	"testing"
	"time"
)

// A trend bucket whose scan fails is silently dropped, so a total that is lower
// than the row count proves rows vanished. AVG() returns REAL while the bucket's
// latency field is an integer, and scanning REAL into *int64 errors — which is
// exactly how every bucket with a recorded latency disappeared.
func TestGetUsageTrendSinceKeepsEveryRow(t *testing.T) {
	database, err := db.OpenDatabase(t.TempDir() + "/t.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepo(database)
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	// All three rows must land in the same hour bucket, otherwise the bucket's
	// mean is a single row and the assertion measures bucket boundaries
	// instead of the REAL-to-int scan it is meant to guard.
	ts := now.Add(-time.Minute).Format(time.RFC3339)
	// The average must be fractional: a whole-number mean converts to int64
	// losslessly and the driver accepts it, so the test would pass even with
	// the bug present.
	latencies := []int{1000, 1100, 1201}
	for _, lat := range latencies {
		meta := fmt.Sprintf(`{"latencyMs":%d,"ttftMs":100}`, lat)
		if _, err := database.Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES (?, 'prov', 'm', 10, 5, 0.01, 'success', '{"cached_tokens":3}', ?)`, ts, meta,
		); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now.Add(-24 * time.Hour).Format(time.RFC3339)
	rows, err := repo.GetUsageTrendSince(cutoff, false)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, row := range rows {
		total += row.Requests
	}
	if total != len(latencies) {
		t.Errorf("trend dropped rows: counted %d of %d (buckets=%d)", total, len(latencies), len(rows))
	}
	if len(rows) > 0 && rows[0].AvgLatencyMs != 1100 {
		t.Errorf("AvgLatencyMs = %d, want 1100", rows[0].AvgLatencyMs)
	}
}

// The chart's x-axis must cover every bucket in the window: a dropped empty
// hour collapses the axis and a quiet stretch reads as a spike.
func TestBuildTrendDensifiesBuckets(t *testing.T) {
	now := time.Date(2026, 9, 29, 14, 30, 0, 0, time.UTC)

	got := buildTrend([]db.UsageTrend{
		{Timestamp: "2026-09-29T09:00:00Z", Requests: 5, Cost: 1.5, AvgLatencyMs: 900},
	}, "today", now)

	if len(got) != 15 {
		t.Fatalf("today: want 15 hourly buckets (00:00..14:00), got %d", len(got))
	}
	if got[0].Timestamp != "2026-09-29T00:00:00Z" || got[14].Timestamp != "2026-09-29T14:00:00Z" {
		t.Errorf("bounds wrong: first=%s last=%s", got[0].Timestamp, got[14].Timestamp)
	}
	if got[9].Requests != 5 || got[9].Cost != 1.5 || got[9].AvgLatencyMs != 900 {
		t.Errorf("row not joined onto its bucket: %+v", got[9])
	}
	if got[8].Requests != 0 {
		t.Errorf("empty bucket should be zero, got %+v", got[8])
	}

	daily := buildTrend(nil, "7d", now)
	if len(daily) != 7 {
		t.Fatalf("7d: want 7 daily buckets, got %d", len(daily))
	}
	if daily[0].Timestamp != "2026-09-23T00:00:00Z" || daily[6].Timestamp != "2026-09-29T00:00:00Z" {
		t.Errorf("7d bounds wrong: first=%s last=%s", daily[0].Timestamp, daily[6].Timestamp)
	}
}