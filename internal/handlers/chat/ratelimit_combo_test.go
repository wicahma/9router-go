package chat

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/ratelimit"
)

// When every model in a combo is over budget, the client must get 429, not
// 502. The combo loops skip a throttled model with a bare `break` and never
// record the denial in lastErr, so the terminal path falls through to "all
// combo models failed" — a status the client cannot distinguish from a genuine
// upstream outage, and the one it would retry immediately. This test is the
// guard for that gap.
func TestComboWithEveryModelThrottledAnswers429(t *testing.T) {
	h, database, cleanup := setupRpsHandler(t)
	defer cleanup()

	seedConnDB(t, database, "openai", "conn-combo-429", "sk-test", "http://127.0.0.1:1")

	const combo = "rps-combo"
	modelsJSON, err := json.Marshal([]string{"openai/gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (?, ?, 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		"combo-rps-429", combo, string(modelsJSON)); err != nil {
		t.Fatalf("seed combo: %v", err)
	}

	ratelimit.Shared().Load(map[string]int{"openai/gpt-4o": 1})
	if ok, _ := ratelimit.Shared().Allow("openai/gpt-4o"); !ok {
		t.Fatal("precondition: the first request must be allowed")
	}
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })

	body := []byte(`{"model":"` + combo + `","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; a 502 hides an exhausted budget behind a generic upstream error: %s",
			rec.Code, rec.Body.String())
	}
}
