package handlerutil

import (
	"context"
	"testing"
)

// The fallback loop rebuilds forwardRequestParams per connection but reuses the
// request context, so every forward mutates one counter. The whole field is
// worthless if each forward saw only itself.
func TestAttemptCounter_AccumulatesOnSharedRequestContext(t *testing.T) {
	ctx := WithAttemptCounter(context.Background())
	for i := 1; i <= 3; i++ {
		CountAttempt(ctx)
		if got := GetAttempts(ctx); got != i {
			t.Fatalf("after %d forwards = %d, want %d", i, got, i)
		}
	}
}

// tryForwardWithConnection wraps the context with WithUsageCapture on the way
// in. Values resolve up the parent chain, so the counter must stay reachable
// and a write through a child must still be visible to the parent.
func TestAttemptCounter_SurvivesChildContextWrap(t *testing.T) {
	ctx := WithAttemptCounter(context.Background())
	CountAttempt(ctx)

	child := context.WithValue(ctx, struct{ k string }{"sentinel"}, true)
	CountAttempt(child)

	if got := GetAttempts(child); got != 2 {
		t.Errorf("child sees %d attempts, want 2", got)
	}
	if got := GetAttempts(ctx); got != 2 {
		t.Errorf("parent sees %d attempts, want 2 — a child write must not be invisible", got)
	}
}
