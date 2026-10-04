package dashboard

import "testing"

// The request path reads the ceiling straight out of the settings row on every
// forward (no registry, unlike modelRps), so this only has to prove the
// dashboard write lands where that read looks. A separate refresh hook would be
// a second place to forget.
func TestSettingsWriteStoresContextLimitForTheRequestPath(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelContextLimit":{"custom/local-llama":8192}}`)

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ModelContextLimit["custom/local-llama"]; got != 8192 {
		t.Fatalf("ModelContextLimit[custom/local-llama] = %d, want 8192", got)
	}
}

// An unrelated save must not clear a ceiling the operator set: the same
// merge-don't-replace rule the other settings keys follow.
func TestUnrelatedSettingsWriteKeepsContextLimit(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelContextLimit":{"custom/local-llama":8192}}`)
	writeSetting(t, router, `{"rtkEnabled":true}`)

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ModelContextLimit["custom/local-llama"]; got != 8192 {
		t.Fatalf("ModelContextLimit[custom/local-llama] = %d, want it untouched at 8192", got)
	}
}

// An empty map is how the UI removes a ceiling ("auto"), so it has to clear.
func TestEmptyContextLimitMapClearsCeilings(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelContextLimit":{"custom/local-llama":8192}}`)
	writeSetting(t, router, `{"modelContextLimit":{}}`)

	s, err := repo.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ModelContextLimit) != 0 {
		t.Fatalf("ModelContextLimit = %v, want all cleared", s.ModelContextLimit)
	}
}
