package chat

import (
	"errors"
	"fmt"
	"testing"
)

// A local RPS denial must never look like an upstream 429. providers.
// RetryableStatusCodes contains 429, and both the fallback loop and the combo
// loop respond to it by LOCKING the connection with a backoff cooldown. That
// would punish a perfectly healthy connection for a limit the operator set
// themselves, so the denial needs its own sentinel and its own handling.
func TestRateLimitDenialIsNotAnUpstream429(t *testing.T) {
	err := &rateLimitError{key: "openai/gpt-4o", retryAfterMs: 100}

	var ue *upstreamError
	if errors.As(err, &ue) {
		t.Fatalf("rate limit denial must not unwrap to *upstreamError, got status %d", ue.StatusCode)
	}
}

func TestRateLimitDenialCarriesItsKeyAndWait(t *testing.T) {
	err := &rateLimitError{key: "openai/gpt-4o", retryAfterMs: 250}

	if !errors.Is(err, errRateLimited) {
		t.Fatal("denial must match the errRateLimited sentinel so callers can branch on it")
	}
	if err.key != "openai/gpt-4o" {
		t.Fatalf("key = %q, want openai/gpt-4o", err.key)
	}
	if err.retryAfterMs != 250 {
		t.Fatalf("retryAfterMs = %d, want 250", err.retryAfterMs)
	}
}

// isRateLimited must be true only for the local denial. A real upstream 429
// is an upstreamError and has to keep going down the retryable path.
func TestIsRateLimitedDistinguishesLocalDenialFromUpstream429(t *testing.T) {
	if !isRateLimited(&rateLimitError{key: "a", retryAfterMs: 1}) {
		t.Fatal("local denial must be recognised as rate limited")
	}
	if isRateLimited(&upstreamError{StatusCode: 429, Body: []byte("upstream says no")}) {
		t.Fatal("a genuine upstream 429 must NOT be treated as a local denial")
	}
	if isRateLimited(errors.New("some other failure")) {
		t.Fatal("unrelated errors must not be rate limited")
	}
}

func TestRateLimitDenialSurvivesWrapping(t *testing.T) {
	// The denial travels back out of tryForwardWithConnection, and callers
	// may wrap it on the way, so the sentinel has to stay reachable.
	err := fmt.Errorf("forward %s: %w", "openai/gpt-4o", &rateLimitError{key: "openai/gpt-4o", retryAfterMs: 100})
	if !isRateLimited(err) {
		t.Fatal("a wrapped local denial must still be recognised")
	}
}
