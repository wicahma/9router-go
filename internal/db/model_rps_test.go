package db

import "testing"

func TestModelRpsRoundTrip(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	if _, err := database.Exec(
		`INSERT OR REPLACE INTO settings (id, data) VALUES (1, ?)`,
		`{"modelRps":{"openai/gpt-4o":10}}`,
	); err != nil {
		t.Fatal(err)
	}

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ModelRps["openai/gpt-4o"]; got != 10 {
		t.Fatalf("ModelRps[openai/gpt-4o] = %d, want 10", got)
	}
}

func TestModelRpsDefaultsUnlimited(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ModelRps) != 0 {
		t.Fatalf("ModelRps = %v, want empty (no limit) by default", s.ModelRps)
	}
}

func TestModelRpsIgnoresNonPositiveValues(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepo(database)

	if _, err := database.Exec(
		`INSERT OR REPLACE INTO settings (id, data) VALUES (1, ?)`,
		`{"modelRps":{"a":0,"b":-5,"c":7}}`,
	); err != nil {
		t.Fatal(err)
	}

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ModelRps["a"]; ok {
		t.Fatal("a 0 rps entry must not be stored")
	}
	if _, ok := s.ModelRps["b"]; ok {
		t.Fatal("a negative rps entry must not be stored")
	}
	if got := s.ModelRps["c"]; got != 7 {
		t.Fatalf("ModelRps[c] = %d, want 7", got)
	}
}
