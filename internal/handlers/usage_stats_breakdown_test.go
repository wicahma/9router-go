package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/dbtest"
	json "encoding/json/v2"
	"net/http/httptest"
	"testing"
)

// The breakdown table's Account / API key / Endpoint tabs are backed by these
// three maps. They were previously either never aggregated (byApiKey,
// byEndpoint => empty table) or keyed on model (byAccount => one row per model
// per account, with a model name printed in the Account column).
func TestUsageStatsBreakdownDimensions(t *testing.T) {
	database, err := db.OpenDatabase(t.TempDir() + "/u.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepo(database)
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatal(err)
	}

	rows := []struct {
		ts, model, conn, endpoint, apiKey string
	}{
		{"2026-09-29T10:00:00Z", "m-one", "conn-1", "/v1/chat/completions", "sk-aaaa1111bbbb2222"},
		{"2026-09-29T11:00:00Z", "m-two", "conn-1", "/v1/chat/completions", "sk-aaaa1111bbbb2222"},
	}
	for _, r := range rows {
		if _, err := database.Exec(
			`INSERT INTO usageHistory (timestamp, provider, model, connectionId, endpoint, apiKey, promptTokens, completionTokens, cost, status, tokens, meta)
			 VALUES (?, 'prov-a', ?, ?, ?, ?, 10, 5, 0.01, 'success', '{}', '{}')`,
			r.ts, r.model, r.conn, r.endpoint, r.apiKey,
		); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	HandleUsageStats(repo).ServeHTTP(rec, httptest.NewRequest("GET", "/usage/stats?period=today", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		ByAccount  map[string]any `json:"byAccount"`
		ByApiKey   map[string]any `json:"byApiKey"`
		ByEndpoint map[string]any `json:"byEndpoint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	// One row per account, not one per (model, account) pair.
	if len(got.ByAccount) != 1 {
		t.Errorf("byAccount: want 1 row for one connection, got %d (%v)", len(got.ByAccount), got.ByAccount)
	}
	if len(got.ByApiKey) == 0 {
		t.Error("byApiKey is empty — the API key tab renders nothing")
	}
	if len(got.ByEndpoint) == 0 {
		t.Error("byEndpoint is empty — the endpoint tab renders nothing")
	}
}
