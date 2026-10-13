package chat

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestAcquireRefreshRoundTrip(t *testing.T) {
	const id = "test-round-trip-conn"
	defer releaseRefresh(id)

	if !acquireRefresh(id) {
		t.Fatal("first acquire = false, want true")
	}
	if acquireRefresh(id) {
		t.Fatal("second acquire = true, want false")
	}
	releaseRefresh(id)
	if !acquireRefresh(id) {
		t.Fatal("acquire after release = false, want true")
	}
}

func TestReleaseRefreshUnknownNoop(t *testing.T) {
	// Must not panic.
	releaseRefresh("test-never-acquired-conn")
	releaseRefresh("")
}

func TestAcquireRefreshEmptyID(t *testing.T) {
	if acquireRefresh("") {
		t.Fatal("acquire with empty connID = true, want false")
	}
}

func TestAcquireRefreshConcurrency(t *testing.T) {
	const id = "test-race-conn"
	defer releaseRefresh(id)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if acquireRefresh(id) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("winners = %d, want exactly 1", got)
	}
}

func TestAcquireRefreshDifferentIDs(t *testing.T) {
	const a = "test-conn-a"
	const b = "test-conn-b"
	defer releaseRefresh(a)
	defer releaseRefresh(b)

	if !acquireRefresh(a) {
		t.Fatal("acquire a = false, want true")
	}
	if !acquireRefresh(b) {
		t.Fatal("acquire b = false while a held, want true")
	}
}
