package ratelimit

import "testing"

// The RPS ceilings live in the global settings row, so every handler in the
// process must throttle against the same buckets. A per-handler registry
// would let N handlers each spend the full budget.
func TestSharedIsASingleton(t *testing.T) {
	a := Shared()
	b := Shared()
	if a != b {
		t.Fatal("Shared() must return the same registry every call")
	}
}

func TestLoadReplacesLimits(t *testing.T) {
	r := New()
	r.SetLimit("a", 100)
	r.SetLimit("b", 100)
	r.Load(map[string]int{"b": 5})
	if ok, _ := r.Allow("a"); !ok {
		t.Fatal("a was removed from config and must be unlimited")
	}
	allowed := 0
	for range 10 {
		if ok, _ := r.Allow("b"); ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed = %d, want 5 (the newly loaded limit)", allowed)
	}
}

func TestLoadEmptyConfigClearsEverything(t *testing.T) {
	r := New()
	r.SetLimit("a", 1)
	r.Load(nil)
	if ok, _ := r.Allow("a"); !ok {
		t.Fatal("Load(nil) must clear all limits, not keep stale ones")
	}
}
