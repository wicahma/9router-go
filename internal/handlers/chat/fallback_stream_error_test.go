package chat

import (
	"context"
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// A streaming upstream that rejects a request often answers HTTP 200 with an
// error object in the body. Before the peek, that body reached the client as a
// successful empty stream and the account/combo fallback never fired. These
// tests pin the fallback to the next connection for both shapes the upstream
// can use: an SSE content type (peek path) and a plain JSON content type
// (ClassifyErrorBody path).

const errBodyJSON = `{"error":{"message":"quota exhausted","type":"rate_limit_error","code":429}}`

func seedConnPriority(t *testing.T, database *sql.DB, connID, baseURL string, priority int) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"apiKey": "sk-" + connID, "baseUrl": baseURL})
	q := `INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES (?, 'deepseek', 'apikey', 'Test', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`
	if _, err := database.Exec(q, connID, priority, string(data)); err != nil {
		t.Fatalf("seed connection %s: %v", connID, err)
	}
}

func TestAccountFallback_StreamErrorBody_FallsToNextConnection(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{"sse content type", "text/event-stream"},
		{"json content type", "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var badHits, goodHits atomic.Int32

			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				badHits.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(errBodyJSON))
			}))
			defer bad.Close()

			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				goodHits.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\n"))
				w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer good.Close()

			database, cleanup := setupChatTestDB(t)
			defer cleanup()
			if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
				t.Fatalf("clear seeded connections: %v", err)
			}
			seedConnPriority(t, database, "conn-bad", bad.URL, 1)
			seedConnPriority(t, database, "conn-good", good.URL, 2)

			repo := db.NewRepo(database)
			h := NewChatHandler(repo)

			body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"stream":true}`)
			rec := httptest.NewRecorder()
			err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, true, false, "/v1/chat/completions")
			if err != nil {
				t.Fatalf("fallback did not recover: %v", err)
			}
			if got := badHits.Load(); got != 1 {
				t.Errorf("expected first connection hit once, got %d", got)
			}
			if got := goodHits.Load(); got != 1 {
				t.Errorf("expected second connection to serve the request, got %d hits", got)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "recovered") {
				t.Errorf("client did not receive the recovered stream: %q", rec.Body.String())
			}
		})
	}
}
