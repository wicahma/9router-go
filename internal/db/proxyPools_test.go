package db

import (
	json "encoding/json/v2"
	"sync"
	"testing"
)

// TestInsertProxyPool_DataShape verifies InsertProxyPool writes the proxyPools
// row with the same data JSON shape and values the Next.js dashboard stores, so
// the shared DB stays compatible.
func TestInsertProxyPool_DataShape(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("failed to create proxyPools table: %v", err)
	}

	repo := NewRepo(db)
	pool, err := repo.InsertProxyPool(ProxyPoolData{
		Name: "relay-x", ProxyURL: "https://relay-x.example.com", NoProxy: "", Type: "vercel", StrictProxy: false,
	})
	if err != nil {
		t.Fatalf("InsertProxyPool failed: %v", err)
	}
	if pool["id"].(string) == "" {
		t.Fatal("expected non-empty generated id")
	}
	if pool["isActive"] != true {
		t.Errorf("expected isActive=true, got %v", pool["isActive"])
	}

	var row struct {
		IsActive   int
		TestStatus string
		Data       string
		CreatedAt  string
		UpdatedAt  string
	}
	err = db.QueryRow(`SELECT isActive, testStatus, data, createdAt, updatedAt FROM proxyPools WHERE id = ?`, pool["id"]).
		Scan(&row.IsActive, &row.TestStatus, &row.Data, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if row.IsActive != 1 {
		t.Errorf("expected isActive column 1, got %d", row.IsActive)
	}
	if row.TestStatus != "unknown" {
		t.Errorf("expected testStatus 'unknown', got %q", row.TestStatus)
	}
	if row.CreatedAt != row.UpdatedAt {
		t.Errorf("expected createdAt == updatedAt, got %q vs %q", row.CreatedAt, row.UpdatedAt)
	}

	// Parse the stored data JSON and assert it matches Next's createProxyPool shape.
	var data map[string]any
	if err := json.Unmarshal([]byte(row.Data), &data); err != nil {
		t.Fatalf("parse stored data: %v", err)
	}
	want := map[string]any{
		"name":         "relay-x",
		"proxyUrl":     "https://relay-x.example.com",
		"noProxy":      "",
		"type":         "vercel",
		"strictProxy":  false,
		"lastTestedAt": nil,
		"lastError":    nil,
	}
	if len(data) != len(want) {
		t.Fatalf("expected %d keys in data, got %d: %v", len(want), len(data), data)
	}
	for k, v := range want {
		if got, ok := data[k]; !ok || got != v {
			t.Errorf("data[%q] = %v (present=%v), want %v", k, got, ok, v)
		}
	}
}

func TestGetProxyPool_SingleProxyUrl_AndMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		data TEXT,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		createdAt TEXT,
		updatedAt TEXT
	);`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	repo := NewRepo(db)

	// Single proxyUrl string (Next.js format)
	poolData := `{"name":"relay-pool","proxyUrl":"https://relay.example.com","type":"vercel","noProxy":"localhost,*.internal","strictProxy":true}`
	if _, err := db.Exec(`INSERT INTO proxyPools (id, data, isActive) VALUES (?, ?, ?)`, "pool-single", poolData, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}

	pool, err := repo.GetProxyPool("pool-single")
	if err != nil {
		t.Fatalf("GetProxyPool failed: %v", err)
	}
	if pool == nil {
		t.Fatal("expected pool, got nil")
	}
	if len(pool.URLs) != 1 || pool.URLs[0] != "https://relay.example.com" {
		t.Errorf("expected URLs [https://relay.example.com], got %v", pool.URLs)
	}
	if pool.Type != "vercel" {
		t.Errorf("expected Type vercel, got %s", pool.Type)
	}
	if pool.NoProxy != "localhost,*.internal" {
		t.Errorf("expected NoProxy localhost,*.internal, got %s", pool.NoProxy)
	}
	if !pool.StrictProxy {
		t.Errorf("expected StrictProxy true, got %v", pool.StrictProxy)
	}
	if next := pool.NextURL(); next != "https://relay.example.com" {
		t.Errorf("expected NextURL https://relay.example.com, got %s", next)
	}
}

// A blip (1-2 consecutive fails) never sidelines; the 3rd parks the URL.
func TestLiveURLs_CooldownAfterThreeFails(t *testing.T) {
	p := &ProxyPool{ID: "p1", URLs: []string{"http://a:8080", "http://b:8080"}}
	p.ReportURLResult("http://a:8080", false)
	p.ReportURLResult("http://a:8080", false)
	if got := p.LiveURLs(); len(got) != 2 {
		t.Fatalf("blip must stay live, got %v", got)
	}
	p.ReportURLResult("http://a:8080", false)
	got := p.LiveURLs()
	if len(got) != 1 || got[0] != "http://b:8080" {
		t.Fatalf("after 3 fails a:8080 must cool down, got %v", got)
	}
	// OK clears the park immediately.
	p.ReportURLResult("http://a:8080", true)
	if got := p.LiveURLs(); len(got) != 2 {
		t.Fatalf("OK must clear cooldown, got %v", got)
	}
}

// All cooling down returns the full list — maybe-dead beats no proxy.
func TestLiveURLs_AllCoolingReturnsAll(t *testing.T) {
	p := &ProxyPool{ID: "p2", URLs: []string{"http://a:8080", "http://b:8080"}}
	for _, u := range p.URLs {
		for i := 0; i < 3; i++ {
			p.ReportURLResult(u, false)
		}
	}
	if got := p.LiveURLs(); len(got) != 2 {
		t.Fatalf("all cooling must return full list, got %v", got)
	}
}

func TestNextURL_SkipsCoolingDown(t *testing.T) {
	p := &ProxyPool{ID: "p3", URLs: []string{"http://dead:8080", "http://live:8080"}}
	for i := 0; i < 3; i++ {
		p.ReportURLResult("http://dead:8080", false)
	}
	for i := 0; i < 4; i++ {
		if got := p.NextURL(); got != "http://live:8080" {
			t.Fatalf("NextURL must skip cooling URL, got %s", got)
		}
	}
}

func TestReportURLResult_ConcurrentSafe(t *testing.T) {
	p := &ProxyPool{ID: "p4", URLs: []string{"http://a:8080", "http://b:8080"}}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := p.URLs[i%2]
			p.ReportURLResult(u, i%3 != 0)
			_ = p.LiveURLs()
			_ = p.NextURL()
		}(i)
	}
	wg.Wait()
}
