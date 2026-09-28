package chat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/ratelimit"
)

// When every model in the chain is over budget, the client must get a real
// 429 with a real Retry-After, not a generic 502 "upstream error". Otherwise
// client-side retry logic (which keys on 429) cannot see it.
func TestRpsExhaustionSurfacesAs429WithRetryAfter(t *testing.T) {
	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()
	seedConnDB(t, database, "openai", "conn-429", "sk-test", "http://127.0.0.1:1")

	const model = "gpt-4o"
	ratelimit.Shared().Load(map[string]int{"openai/" + model: 1})
	ratelimit.Shared().Allow("openai/" + model)
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })

	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (a 502 would be indistinguishable from a real upstream failure)", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After must be set so the client knows when to come back")
	}
}

func TestWriteRpsErrorEmitsJsonBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRpsError(rec, newRateLimitError("openai/gpt-4o", 250*time.Millisecond))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json so SDKs parse the error", ct)
	}
	// 250ms rounds up to 1 second: Retry-After is integer seconds, and
	// rounding down would invite an immediate retry that gets denied again.
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1 (250ms rounds up, never down)", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "gpt-4o") {
		t.Fatalf("body %q must name the model that ran out, so the operator knows which limit to raise", body)
	}
}
