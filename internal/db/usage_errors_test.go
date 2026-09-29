package db

import (
	"9router/proxy/internal/dbtest"
	"database/sql"
	"fmt"
	"testing"
)

// Failures are recorded in requestDetails, not usageHistory: the success path
// writes usageHistory and its status column only ever holds 'ok'/'success', so
// reading failures from there yields nothing. usageHistory is seeded with a
// successful row here to prove the error queries ignore it.
func seedErrors(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatal(err)
	}
	// succeeded request, must never be counted as a failure
	if _, err := database.Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES (datetime('now'), 'p', 'ok-model', 1, 1, 0, 'success', '{}', '{"attempts":1}')`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		provider, model string
		status          int
		attempts        int
	}{
		{"node-a", "m-one", 429, 3},
		{"node-a", "m-one", 429, 2},
		{"node-b", "m-two", 500, 11},
		{"node-b", "m-three", 400, 1},
	}
	for i, r := range rows {
		data := fmt.Sprintf(`{"response":{"status":%d},"attempts":%d}`, r.status, r.attempts)
		if _, err := database.Exec(
			`INSERT INTO requestDetails (id, timestamp, provider, model, connectionId, status, data)
			 VALUES (?, datetime('now'), ?, ?, 'c1', 'error', ?)`,
			fmt.Sprintf("id-%d-%s-%d", r.status, r.model, i), r.provider, r.model, data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetErrorStatsSinceReadsRequestDetails(t *testing.T) {
	database, err := OpenDatabase(t.TempDir() + "/e.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(database)
	seedErrors(t, database)

	stats, err := repo.GetErrorStatsSince("2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 4 {
		t.Fatalf("Total = %d, want 4 failures (the successful usageHistory row must not count)", stats.Total)
	}
	if len(stats.ByStatus) == 0 || stats.ByStatus[0].Key != "429" || stats.ByStatus[0].Count != 2 {
		t.Errorf("ByStatus = %+v, want 429 first with count 2", stats.ByStatus)
	}
	if len(stats.ByModel) == 0 || stats.ByModel[0].Key != "m-one" {
		t.Errorf("ByModel = %+v, want m-one first", stats.ByModel)
	}
	if len(stats.ByProvider) == 0 || stats.ByProvider[0].Key != "node-a" {
		t.Errorf("ByProvider = %+v, want node-a first", stats.ByProvider)
	}
}

func TestGetAttemptDistributionSince(t *testing.T) {
	database, err := OpenDatabase(t.TempDir() + "/a.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(database)
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatal(err)
	}

	for _, attempts := range []int{1, 1, 1, 4} {
		meta := fmt.Sprintf(`{"attempts":%d,"latencyMs":900}`, attempts)
		if _, err := database.Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES (datetime('now'), 'p', 'm', 1, 1, 0, 'success', '{}', ?)`, meta); err != nil {
			t.Fatal(err)
		}
	}
	// A row with no attempt count must be ignored, not folded into bucket 0.
	if _, err := database.Exec(
		`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, cost, status, tokens, meta)
		 VALUES (datetime('now'), 'p', 'm', 1, 1, 0, 'success', '{}', '{}')`); err != nil {
		t.Fatal(err)
	}

	buckets, err := repo.GetAttemptDistributionSince("2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]int{}
	for _, b := range buckets {
		got[b.Attempts] = b.Requests
	}
	if got[1] != 3 || got[4] != 1 {
		t.Errorf("distribution = %+v, want {1:3, 4:1}", got)
	}
	if _, ok := got[0]; ok {
		t.Errorf("rows without an attempt count leaked into bucket 0: %+v", got)
	}
}
