package chat

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/ratelimit"
)

func setupRpsHandler(t *testing.T) (*ChatHandler, *sql.DB, func()) {
	t.Helper()
	database, cleanupDB := setupChatTestDB(t)
	h := NewChatHandler(db.NewRepo(database))
	return h, database, cleanupDB
}

// The whole point of a separate denial type: a model that runs out of local
// RPS budget must not get its connection locked with a backoff cooldown. A
// lock is a statement about connection health, and the operator's own limit
// says nothing about health.
//
// TWO connections are seeded deliberately. With one, the fallback loop runs
// out of candidates immediately and returns without ever reaching the
// retryable branch, so the test would pass whether or not the guard exists —
// it could not tell a working guard from a missing one. The second connection
// is what makes the lock path reachable, and therefore what makes this test
// able to fail.
func TestRpsDenialDoesNotLockTheConnection(t *testing.T) {
	var upstreamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()

	seedConnDB(t, database, "openai", "conn-rps", "sk-test", srv.URL)
	seedConnDB(t, database, "openai", "conn-rps-2", "sk-test", srv.URL)

	// One token, already spent: the next forward is denied locally.
	const model = "gpt-4o"
	ratelimit.Shared().Load(map[string]int{"openai/" + model: 1})
	if ok, _ := ratelimit.Shared().Allow("openai/" + model); !ok {
		t.Fatal("precondition: the first request should be allowed")
	}
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })

	err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(),
		"openai", model, "", []byte(`{"messages":[{"role":"user","content":"hi"}]}`), false, false, "/v1/chat/completions")
	if err == nil {
		t.Fatal("want a rate limit error, got nil")
	}
	if !isRateLimited(err) {
		t.Fatalf("want a rate limit denial, got %v", err)
	}
	if upstreamHits != 0 {
		t.Fatalf("upstream was hit %d times; a denied request must never leave the process", upstreamHits)
	}

	for _, connID := range []string{"conn-rps", "conn-rps-2"} {
		locked, lockErr := h.Repo.IsConnectionModelLocked(connID, model)
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		if locked {
			t.Fatalf("%s was locked by a local RPS denial — it must stay healthy", connID)
		}
	}
}

// The denial has to reach the caller intact, carrying the model and a real
// wait, so the client gets a 429 with a real Retry-After instead of a generic
// upstream failure.
func TestRpsDenialCarriesRetryAfter(t *testing.T) {
	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()

	seedConnDB(t, database, "openai", "conn-ra", "sk-test", "http://127.0.0.1:1")

	const model = "gpt-5"
	ratelimit.Shared().Load(map[string]int{"openai/" + model: 1})
	ratelimit.Shared().Allow("openai/" + model)
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })

	err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(),
		"openai", model, "", []byte(`{"messages":[]}`), false, false, "/v1/chat/completions")

	var rl *rateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("want a *rateLimitError, got %v", err)
	}
	if rl.retryAfterMs < 1 {
		t.Fatalf("retryAfterMs = %d, want a positive hint", rl.retryAfterMs)
	}
	if rl.key != "openai/"+model {
		t.Fatalf("key = %q, want openai/%s", rl.key, model)
	}
}

// A model with no configured limit must be completely unaffected. This is the
// regression guard for the limiter costing the hot path nothing when unused.
func TestNoLimitConfiguredMeansNoThrottling(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()
	seedConnDB(t, database, "openai", "conn-free", "sk-test", srv.URL)
	ratelimit.Shared().Load(nil)

	for range 50 {
		if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(),
			"openai", "unlimited-model", "", []byte(`{"messages":[]}`), false, false, "/v1/chat/completions"); err != nil {
			t.Fatalf("unexpected error with no limit configured: %v", err)
		}
	}
	if hits != 50 {
		t.Fatalf("upstream hits = %d, want 50 — an unlimited model must not be throttled", hits)
	}
}

// Buckets are keyed per model, so draining one model must leave a sibling on
// the same provider working. This is the case the feature exists for: several
// models on one throttled subscription, only some of them over budget.
func TestExhaustedModelDoesNotThrottleItsSibling(t *testing.T) {
	var upstreamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()
	seedConnDB(t, database, "deepseek", "conn-sib", "sk-test", srv.URL)

	ratelimit.Shared().Load(map[string]int{"deepseek/cheap-model": 1})
	ratelimit.Shared().Allow("deepseek/cheap-model")
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })

	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(),
		"deepseek", "cheap-model", "", []byte(`{"messages":[]}`), false, false, "/v1/chat/completions"); !isRateLimited(err) {
		t.Fatalf("cheap-model should be denied, got %v", err)
	}
	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(),
		"deepseek", "other-model", "", []byte(`{"messages":[]}`), false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("other-model shares the provider but not the limit, want success, got %v", err)
	}
	if upstreamHits != 1 {
		t.Fatalf("upstream hits = %d, want exactly 1 (the denied model must not reach it)", upstreamHits)
	}
}
