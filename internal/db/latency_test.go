package db

import (
	"database/sql"
	"fmt"
	"testing"
)

// mustCreateUsageHistory builds the usageHistory table used by the latency
// tests. The shared schema helper is not used here because these tests need
// explicit control over timestamps, which InsertUsageHistory hardcodes to now.
func mustCreateUsageHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS usageHistory (
		timestamp TEXT,
		provider TEXT,
		model TEXT,
		connectionId TEXT,
		apiKey TEXT,
		endpoint TEXT,
		promptTokens INTEGER,
		completionTokens INTEGER,
		cost REAL,
		status TEXT,
		tokens TEXT,
		meta TEXT
	);`)
	if err != nil {
		t.Fatalf("create usageHistory: %v", err)
	}
}

func insertLatencyRows(t *testing.T, db *sql.DB, model, provider, ts string, latencies []int) {
	t.Helper()
	for _, ms := range latencies {
		meta := fmt.Sprintf(`{"latencyMs":%d,"ttftMs":1,"httpStatus":200,"streamed":true}`, ms)
		if _, err := db.Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES (?, ?, ?, 1, 1, 0, 'success', '{}', ?)`,
			ts, provider, model, meta,
		); err != nil {
			t.Fatalf("insert latency row: %v", err)
		}
	}
}

// TestGetLatencyStatsSince_NearestRank verifies the percentile ranks against a
// hand-checkable sample: 100 distinct values, so p50/p95/p99 land on the 50th,
// 95th and 99th value.
func TestGetLatencyStatsSince_NearestRank(t *testing.T) {
	raw, cleanup := setupTestDB(t)
	defer cleanup()
	mustCreateUsageHistory(t, raw)

	latencies := make([]int, 0, 100)
	for i := 1; i <= 100; i++ {
		latencies = append(latencies, i*10)
	}
	insertLatencyRows(t, raw, "model-a", "prov-a", "2026-09-27T10:00:00Z", latencies)

	repo := NewRepo(raw)
	got, err := repo.GetLatencyStatsSince("2026-09-27T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetLatencyStatsSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}

	s := got[0]
	if s.Samples != 100 {
		t.Errorf("samples: want 100, got %d", s.Samples)
	}
	if s.P50Ms != 500 {
		t.Errorf("p50: want 500, got %d", s.P50Ms)
	}
	if s.P95Ms != 950 {
		t.Errorf("p95: want 950, got %d", s.P95Ms)
	}
	if s.P99Ms != 990 {
		t.Errorf("p99: want 990, got %d", s.P99Ms)
	}
}

// TestGetLatencyStatsSince_IgnoresRowsWithoutLatency is the important one: the
// vast majority of stored rows predate duration capture. If they counted, every
// percentile would collapse toward zero and the metric would lie.
func TestGetLatencyStatsSince_IgnoresRowsWithoutLatency(t *testing.T) {
	raw, cleanup := setupTestDB(t)
	defer cleanup()
	mustCreateUsageHistory(t, raw)

	insertLatencyRows(t, raw, "model-b", "prov-b", "2026-09-27T10:00:00Z", []int{1000, 2000, 3000})

	// Rows with no meta at all, exactly as written before durations existed.
	for i := 0; i < 50; i++ {
		if _, err := raw.Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES ('2026-09-27T10:00:00Z','prov-b','model-b',1,1,0,'success','{}','')`,
		); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	repo := NewRepo(raw)
	got, err := repo.GetLatencyStatsSince("2026-09-27T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetLatencyStatsSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	if got[0].Samples != 3 {
		t.Errorf("samples: want 3 (only rows with durations), got %d", got[0].Samples)
	}
	if got[0].P50Ms <= 0 {
		t.Errorf("p50 collapsed to zero, legacy rows leaked into the distribution: %d", got[0].P50Ms)
	}
}

func TestGetLatencyStatsSince_RespectsCutoff(t *testing.T) {
	raw, cleanup := setupTestDB(t)
	defer cleanup()
	mustCreateUsageHistory(t, raw)

	insertLatencyRows(t, raw, "model-c", "prov-c", "2026-09-20T10:00:00Z", []int{1111, 2222})
	insertLatencyRows(t, raw, "model-c", "prov-c", "2026-09-27T10:00:00Z", []int{9999})

	repo := NewRepo(raw)
	got, err := repo.GetLatencyStatsSince("2026-09-27T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetLatencyStatsSince: %v", err)
	}
	if len(got) != 1 || got[0].Samples != 1 {
		t.Fatalf("expected 1 group with 1 sample, got %+v", got)
	}
	if got[0].P50Ms != 9999 {
		t.Errorf("p50: want 9999, got %d", got[0].P50Ms)
	}
}

// TestGetLatencyStatsSince_MalformedMetaDoesNotKillTheQuery pins the trust
// boundary: json_extract raises on malformed JSON, so one junk value used to
// take down the whole dashboard metric instead of being skipped.
func TestGetLatencyStatsSince_MalformedMetaDoesNotKillTheQuery(t *testing.T) {
	raw, cleanup := setupTestDB(t)
	defer cleanup()
	mustCreateUsageHistory(t, raw)

	insertLatencyRows(t, raw, "model-d", "prov-d", "2026-09-27T10:00:00Z", []int{100, 200, 300, 400})

	if _, err := raw.Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES ('2026-09-27T10:00:00Z','prov-d','model-d',1,1,0,'success','{}','{"latencyMs":')`,
	); err != nil {
		t.Fatalf("insert malformed row: %v", err)
	}

	repo := NewRepo(raw)
	got, err := repo.GetLatencyStatsSince("2026-09-27T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("malformed meta must be skipped, not fatal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	if got[0].Samples != 4 {
		t.Errorf("samples: want 4 (malformed row excluded), got %d", got[0].Samples)
	}
}

func TestGetLatencyStatsSince_GroupsByModelAndProvider(t *testing.T) {
	raw, cleanup := setupTestDB(t)
	defer cleanup()
	mustCreateUsageHistory(t, raw)

	insertLatencyRows(t, raw, "shared-model", "fast-prov", "2026-09-27T10:00:00Z", []int{100, 200, 300})
	insertLatencyRows(t, raw, "shared-model", "slow-prov", "2026-09-27T10:00:00Z", []int{8000, 9000, 10000})

	repo := NewRepo(raw)
	got, err := repo.GetLatencyStatsSince("2026-09-27T00:00:00Z", 10)
	if err != nil {
		t.Fatalf("GetLatencyStatsSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}

	byProvider := map[string]LatencyStats{}
	for _, s := range got {
		byProvider[s.Provider] = s
	}
	if byProvider["fast-prov"].P50Ms != 200 {
		t.Errorf("fast-prov p50: want 200, got %d", byProvider["fast-prov"].P50Ms)
	}
	if byProvider["slow-prov"].P50Ms != 9000 {
		t.Errorf("slow-prov p50: want 9000, got %d", byProvider["slow-prov"].P50Ms)
	}
}
