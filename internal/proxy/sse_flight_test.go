package proxy

import (
	"context"
	"sync"
	"testing"
	"time"

	"9router/proxy/internal/log"
	"9router/proxy/internal/usagetracker"
)

// TestHeartbeatWriter_MarksStreamPhaseOnFirstByte covers the wiring between the
// streaming writer and the live ops panel: the flight must stay in "upstream"
// until the first real chunk reaches the client, then flip to "stream" exactly
// once. Keep-alives must not count as a first byte, otherwise an idle upstream
// would look like it had started responding.
func TestHeartbeatWriter_MarksStreamPhaseOnFirstByte(t *testing.T) {
	reqID := "req_stream_phase_test"
	ctx := context.WithValue(context.Background(), log.RequestIDKey, reqID)

	usagetracker.StartFlight(reqID, "gpt-4o", "openai", "acct")
	defer usagetracker.EndFlight(reqID)
	usagetracker.SetFlightPhase(reqID, usagetracker.PhaseUpstream, "openai/gpt-4o")

	rec := &mockResponseWriter{}
	// Long interval: no heartbeat fires during the test, so any phase change
	// must come from an explicit Write.
	hw := NewHeartbeatWriter(ctx, rec, time.Hour)

	phase := func() string {
		for _, f := range usagetracker.SnapshotFlights() {
			if f.ID == reqID {
				return f.Phase
			}
		}
		return ""
	}

	if got := phase(); got != usagetracker.PhaseUpstream {
		t.Fatalf("phase before first byte = %q, want %q", got, usagetracker.PhaseUpstream)
	}

	hw.Write([]byte("data: {}\n\n"))
	if got := phase(); got != usagetracker.PhaseStream {
		t.Fatalf("phase after first byte = %q, want %q", got, usagetracker.PhaseStream)
	}

	// A second write must not move the phase again (it is still "stream").
	usagetracker.SetFlightPhase(reqID, usagetracker.PhaseUpstream, "openai/gpt-4o")
	hw.Write([]byte("data: {}\n\n"))
	if got := phase(); got != usagetracker.PhaseUpstream {
		t.Fatalf("phase was re-marked after the first byte: got %q", got)
	}
	hw.Close()
}

// TestHeartbeatWriter_UntrackedContextIsSafe guards the degenerate cases: no
// request ID in the context and a request that was never registered must both
// stream normally rather than panic or block.
func TestHeartbeatWriter_UntrackedContextIsSafe(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), nil} {
		rec := &mockResponseWriter{}
		hw := NewHeartbeatWriter(ctx, rec, time.Hour)
		if _, err := hw.Write([]byte("chunk")); err != nil {
			t.Fatalf("write with ctx=%v failed: %v", ctx, err)
		}
		hw.Close()
	}

	// Request ID present but no flight registered (e.g. a non-upstream path).
	ctx := context.WithValue(context.Background(), log.RequestIDKey, "req_never_registered")
	rec := &mockResponseWriter{}
	hw := NewHeartbeatWriter(ctx, rec, time.Hour)
	if _, err := hw.Write([]byte("chunk")); err != nil {
		t.Fatalf("write for unregistered flight failed: %v", err)
	}
	hw.Close()
}

// TestHeartbeatWriter_ConcurrentWriters is a race-detector target: the
// streamMarked flag must be touched only under the writer's mutex.
func TestHeartbeatWriter_ConcurrentWriters(t *testing.T) {
	reqID := "req_stream_phase_race"
	ctx := context.WithValue(context.Background(), log.RequestIDKey, reqID)
	usagetracker.StartFlight(reqID, "gpt-4o", "openai", "acct")
	defer usagetracker.EndFlight(reqID)

	rec := &mockResponseWriter{}
	hw := NewHeartbeatWriter(ctx, rec, time.Hour)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				hw.Write([]byte("x"))
			}
		}()
	}
	wg.Wait()
	hw.Close()
}
