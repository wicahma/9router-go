package handlerutil

import (
	"context"
	"sync"
	"testing"
)

func TestGetAttempts_WithoutCounterIsOne(t *testing.T) {
	if got := GetAttempts(context.Background()); got != 1 {
		t.Errorf("GetAttempts without a counter = %d, want 1 — a request that reached the forward path tried at least once", got)
	}
	if got := GetAttempts(nil); got != 1 {
		t.Errorf("GetAttempts(nil) = %d, want 1", got)
	}
}

func TestAttemptCounter_CountsAcrossRetries(t *testing.T) {
	ctx := WithAttemptCounter(context.Background())
	if got := GetAttempts(ctx); got != 1 {
		t.Fatalf("before any attempt = %d, want the floor of 1", got)
	}
	CountAttempt(ctx)
	CountAttempt(ctx)
	CountAttempt(ctx)
	if got := GetAttempts(ctx); got != 3 {
		t.Errorf("after 3 forwards = %d, want 3", got)
	}
}

func TestCountAttempt_NilContextAndMissingCounterAreNoOps(t *testing.T) {
	CountAttempt(nil)
	CountAttempt(context.Background()) // no counter installed: must not panic
	if got := GetAttempts(context.Background()); got != 1 {
		t.Errorf("GetAttempts after a no-op count = %d, want 1", got)
	}
}

func TestAttemptCounter_ReinstallingStartsAFreshRequest(t *testing.T) {
	// tryForwardWithConnection re-derives ctx per attempt (WithUsageCapture does
	// the same), so installing again is a new request, not a continuation. The
	// counter must not silently merge counts across that boundary.
	ctx := WithAttemptCounter(context.Background())
	CountAttempt(ctx)
	CountAttempt(ctx)
	if got := GetAttempts(ctx); got != 2 {
		t.Fatalf("first request = %d attempts, want 2", got)
	}
	fresh := WithAttemptCounter(ctx)
	if got := GetAttempts(fresh); got != 1 {
		t.Errorf("reinstalled counter = %d attempts, want a fresh 1", got)
	}
}

func TestAttemptCounter_ParallelForwardsAllCounted(t *testing.T) {
	// Fusion forwards to several connections concurrently on one context.
	ctx := WithAttemptCounter(context.Background())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			CountAttempt(ctx)
		}()
	}
	wg.Wait()
	if got := GetAttempts(ctx); got != 8 {
		t.Errorf("after 8 parallel forwards = %d, want 8 — a non-atomic counter would lose increments", got)
	}
}
