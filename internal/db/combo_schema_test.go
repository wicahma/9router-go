package db

import "testing"

// TestComboWritesAgainstCoreSchema guards the gap between the write SQL and the
// production schema. CreateCombo/UpdateCombo write a contextSize column, but
// that column only ever existed in the hand-rolled CREATE TABLE inside
// dashboard_test.go — the production schema (EnsureCoreSchema) never had it, so
// every save 500'd with "no such column: contextSize" on a real database while
// the fixture-based tests stayed green.
//
// This test deliberately builds the DB through the production schema path so a
// fixture DDL that drifts from schema.go can never hide the bug again.
func TestComboWritesAgainstCoreSchema(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	if err := r.CreateCombo("combo-1", "my-combo", "", `["openai/gpt-4o"]`, "fallback"); err != nil {
		t.Fatalf("CreateCombo against core schema failed: %v", err)
	}

	if err := r.UpdateCombo("combo-1", "my-combo", "", `["openai/gpt-4o","openai/gpt-4o-mini"]`, "fallback"); err != nil {
		t.Fatalf("UpdateCombo against core schema failed: %v", err)
	}

	combos, err := r.GetCombos()
	if err != nil {
		t.Fatalf("GetCombos failed: %v", err)
	}
	if len(combos) != 1 {
		t.Fatalf("expected 1 combo, got %d", len(combos))
	}
}
