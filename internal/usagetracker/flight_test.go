package usagetracker

import (
	"fmt"
	"testing"
	"time"
)

func TestFlights_Lifecycle(t *testing.T) {
	flightsMu.Lock()
	flights = make(map[string]*flightEntry, 64)
	flightsMu.Unlock()

	if got := SnapshotFlights(); len(got) != 0 {
		t.Fatalf("expected empty table, got %d", len(got))
	}

	StartFlight("req_a", "gpt-4o", "openai", "conn_1")
	if got := SnapshotFlights(); len(got) != 1 || got[0].Phase != PhaseQueue {
		t.Fatalf("expected 1 queued flight, got %+v", got)
	}

	SetFlightPhase("req_a", PhaseUpstream, "openai/gpt-4o")
	got := SnapshotFlights()
	if len(got) != 1 || got[0].Phase != PhaseUpstream || got[0].Detail != "openai/gpt-4o" {
		t.Fatalf("phase not applied: %+v", got)
	}

	SetFlightAttempt("req_a", 3)
	if got = SnapshotFlights(); got[0].Attempt != 3 {
		t.Fatalf("attempt not applied: %+v", got[0])
	}

	EndFlight("req_a")
	if got = SnapshotFlights(); len(got) != 0 {
		t.Fatalf("expected empty after end, got %d", len(got))
	}
}

func TestFlights_RepeatedStartDoesNotDuplicate(t *testing.T) {
	flightsMu.Lock()
	flights = make(map[string]*flightEntry, 64)
	flightsMu.Unlock()

	StartFlight("req_b", "m", "p", "c")
	StartFlight("req_b", "m2", "p2", "c2")
	got := SnapshotFlights()
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Model != "m2" || got[0].Provider != "p2" {
		t.Fatalf("restart should refresh identity, got %+v", got[0])
	}
}

func TestFlights_CapAndTTL(t *testing.T) {
	flightsMu.Lock()
	flights = make(map[string]*flightEntry, 64)
	flightsMu.Unlock()

	for i := 0; i < maxFlights+50; i++ {
		StartFlight(fmt.Sprintf("req_%d", i), "m", "p", "c")
	}
	if got := len(flights); got != maxFlights {
		t.Fatalf("expected cap %d, got %d", maxFlights, got)
	}

	// A flight whose owner never reported completion must not live forever.
	flightsMu.Lock()
	flights["req_stale"] = &flightEntry{
		id: "req_stale", phase: PhaseQueue,
		started: time.Now().Add(-2 * flightTTL), phaseAt: time.Now().Add(-2 * flightTTL),
	}
	flightsMu.Unlock()

	for _, f := range SnapshotFlights() {
		if f.ID == "req_stale" {
			t.Fatal("stale flight survived the TTL sweep")
		}
	}
}

// TestFlights_WritersNeverBlock guards the latency contract: a writer must
// return immediately even while the table lock is held, because telemetry in
// the request path must not become a wait.
func TestFlights_WritersNeverBlock(t *testing.T) {
	flightsMu.Lock()
	flights = make(map[string]*flightEntry, 64)
	flightsMu.Unlock()

	flightsMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		StartFlight("req_blocked", "m", "p", "c")
		SetFlightPhase("req_blocked", PhaseDB, "waiting")
		SetFlightAttempt("req_blocked", 1)
		EndFlight("req_blocked")
	}()

	select {
	case <-done:
		// Writers gave up and returned; that is the contract.
	case <-time.After(2 * time.Second):
		flightsMu.Unlock()
		t.Fatal("writers blocked on a held lock; the latency contract is broken")
	}
	flightsMu.Unlock()
}

// BenchmarkFlightPhase measures the added cost on the request path.
func BenchmarkFlightPhase(b *testing.B) {
	flightsMu.Lock()
	flights = make(map[string]*flightEntry, 64)
	flightsMu.Unlock()
	StartFlight("req_bench", "m", "p", "c")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SetFlightPhase("req_bench", PhaseUpstream, "openai/gpt-4o")
		SetFlightPhase("req_bench", PhaseQueue, "retry-after 5s")
	}
}
