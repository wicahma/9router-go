package chat

import (
	"context"
	"testing"

	"9router/proxy/internal/handlerutil"
)

// tryForwardWithConnection is the single forward call site, so a successful
// client request that burned retries must report them. If the counter were only
// meaningful in isolation, the field would always read 1 and this test is the
// only thing standing between that and a silent lie.
func TestAttemptCounterReachesTheUsageRecord(t *testing.T) {
	ctx := handlerutil.WithAttemptCounter(context.Background())
	handlerutil.CountAttempt(ctx) // first connection died
	handlerutil.CountAttempt(ctx) // second died
	handlerutil.CountAttempt(ctx) // third answered

	info := &UsageLogInfo{
		Provider:     "kiro",
		Model:        "claude-sonnet-4",
		ConnectionID: "c9",
		Attempts:     handlerutil.GetAttempts(ctx),
	}
	if info.Attempts != 3 {
		t.Fatalf("attempts on the usage record = %d, want 3", info.Attempts)
	}

	var got usageMeta
	got.Attempts = attemptsFor(info)
	if got.Attempts != 3 {
		t.Errorf("attemptsFor = %d, want 3", got.Attempts)
	}
}
