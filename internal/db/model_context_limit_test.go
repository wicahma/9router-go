package db

import "testing"

// The per-model context ceiling is stored exactly like modelRps (see
// model_rps_test.go): a model -> positive-int map where a missing key means
// "use whatever the provider allows". The hot path trusts that shape without
// re-validating, so these pin it.

func TestModelContextLimitRoundTrip(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	if _, err := database.Exec(
		`INSERT OR REPLACE INTO settings (id, data) VALUES (1, ?)`,
		`{"modelContextLimit":{"custom/local-llama":8192}}`,
	); err != nil {
		t.Fatal(err)
	}

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ModelContextLimit["custom/local-llama"]; got != 8192 {
		t.Fatalf("ModelContextLimit[custom/local-llama] = %d, want 8192", got)
	}
}

func TestModelContextLimitDefaultsUncapped(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ModelContextLimit) != 0 {
		t.Fatalf("ModelContextLimit = %v, want empty (no ceiling) by default", s.ModelContextLimit)
	}
}

// A stored 0 or negative value would read as a real ceiling of zero and trim
// every conversation to nothing, so the parser must drop them on the way in.
func TestModelContextLimitIgnoresNonPositiveValues(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	if _, err := database.Exec(
		`INSERT OR REPLACE INTO settings (id, data) VALUES (1, ?)`,
		`{"modelContextLimit":{"a":0,"b":-5,"c":32768}}`,
	); err != nil {
		t.Fatal(err)
	}

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ModelContextLimit["a"]; ok {
		t.Fatal("a 0 context entry must not be stored")
	}
	if _, ok := s.ModelContextLimit["b"]; ok {
		t.Fatal("a negative context entry must not be stored")
	}
	if got := s.ModelContextLimit["c"]; got != 32768 {
		t.Fatalf("ModelContextLimit[c] = %d, want 32768", got)
	}
}
