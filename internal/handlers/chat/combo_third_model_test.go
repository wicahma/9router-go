package chat

import (
	"context"
	database_sql "database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"9router/proxy/internal/db"
)

// TestComboFallback_429OnOneModelStillTriesTheNextModelOnSameProvider is the
// regression for the reported 3-model/2-provider combo: models 1 and 2 could
// switch but the third model was never attempted, because model 2's 429
// rate-limited and excluded the whole provider-B connection — so the client
// saw an error even though model 3 on the same account was perfectly able to
// serve. A 429 is a per-model quota signal within the request; only auth
// failures (401/403) may exclude the connection for the remaining models.
func TestComboFallback_429OnOneModelStillTriesTheNextModelOnSameProvider(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	var aHits int

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		aHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"provider a quota exhausted","type":"rate_limit_error","code":429}}`))
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.UnmarshalRead(r.Body, &body)
		model, _ := body["model"].(string)
		mu.Lock()
		hits[model]++
		mu.Unlock()

		if model == "m2" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"m2 rate limited","type":"rate_limit_error","code":429}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-m3","object":"chat.completion","created":0,"model":"m3","choices":[{"index":0,"message":{"role":"assistant","content":"third-model-answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}))
	defer srvB.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedConnDB(t, database, "deepseek", "conn-a", "sk-a", srvA.URL)
	seedConnDB(t, database, "groq", "conn-b", "sk-b", srvB.URL)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	comboModels := []string{"deepseek/m1", "groq/m2", "groq/m3"}
	body := []byte(`{"model":"deepseek/m1","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`)
	rec := httptest.NewRecorder()
	h.handleComboFallback(context.Background(), rec, body, comboModels, "fallback", false, false, "combo-third", 0)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected the combo to be served by model 3, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "third-model-answer") {
		t.Errorf("expected model 3's answer in the response, got %s", rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if aHits != 1 {
		t.Errorf("expected exactly 1 hit on provider a, got %d", aHits)
	}
	if hits["m2"] != 1 {
		t.Errorf("expected exactly 1 hit for model m2, got %d", hits["m2"])
	}
	if hits["m3"] != 1 {
		t.Errorf("expected exactly 1 hit for model m3, got %d", hits["m3"])
	}

	// Each failed model must end up locked so a later request cannot hammer
	// the same quota bucket; model 3 served and must stay unlocked.
	assertModelLocked(t, database, "conn-a", "m1")
	assertModelLocked(t, database, "conn-b", "m2")
}

func assertModelLocked(t *testing.T, database *database_sql.DB, connID, model string) {
	t.Helper()
	var data string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = ?`, connID).Scan(&data); err != nil {
		t.Fatalf("read connection %s: %v", connID, err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatalf("parse connection data: %v", err)
	}
	key := "modelLock_" + model
	until, _ := raw[key].(string)
	if until == "" {
		t.Errorf("expected %s to carry an active lock for %s, got none", connID, model)
	}
}
