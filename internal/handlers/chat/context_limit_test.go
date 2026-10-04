package chat

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// msg is a minimal chat message for the trimming tests. content is padded to
// make its char count predictable: role+content keys plus the strings.
func msg(role, content string) map[string]any {
	return map[string]any{"role": role, "content": content}
}

func TestTrimMessagesToBudgetKeepsNewest(t *testing.T) {
	req := map[string]any{"messages": []any{
		msg("user", strings.Repeat("a", 4000)),
		msg("assistant", strings.Repeat("b", 4000)),
		msg("user", "the newest question"),
	}}

	left, dropped := trimMessagesToBudget(req, 200)

	if dropped != 2 {
		t.Fatalf("dropped = %d, want the 2 oldest", dropped)
	}
	kept, _ := req["messages"].([]any)
	if len(kept) != 1 {
		t.Fatalf("kept %d messages, want only the newest", len(kept))
	}
	if got := kept[0].(map[string]any)["content"]; got != "the newest question" {
		t.Fatalf("kept message = %v, want the newest question", got)
	}
	if left != countValueChars(kept[0]) {
		t.Fatalf("left = %d, want the kept message's char count", left)
	}
}

// The last turn is the question being asked and a system/developer turn carries
// the instructions; dropping either produces a request that no longer means what
// the caller sent, so they survive a budget they do not fit in.
func TestTrimMessagesToBudgetProtectsSystemAndLast(t *testing.T) {
	req := map[string]any{"messages": []any{
		msg("system", strings.Repeat("s", 4000)),
		msg("user", strings.Repeat("a", 4000)),
		msg("user", "the newest question"),
	}}

	_, dropped := trimMessagesToBudget(req, 200)

	if dropped != 1 {
		t.Fatalf("dropped = %d, want only the middle message", dropped)
	}
	kept, _ := req["messages"].([]any)
	if len(kept) != 2 {
		t.Fatalf("kept %d messages, want the system turn and the newest", len(kept))
	}
	roles := []string{
		kept[0].(map[string]any)["role"].(string),
		kept[1].(map[string]any)["role"].(string),
	}
	if roles[0] != "system" || roles[1] != "user" {
		t.Fatalf("kept roles = %v, want [system user]", roles)
	}
}

// Top-level tools/system are part of the input the provider measures, so their
// chars come off the budget before any message does.
func TestTrimMessagesToBudgetCountsTopLevelFields(t *testing.T) {
	req := map[string]any{
		"system": strings.Repeat("s", 300),
		"messages": []any{
			msg("user", strings.Repeat("a", 4000)),
			msg("user", "newest"),
		},
	}

	_, dropped := trimMessagesToBudget(req, 200)

	if dropped != 1 {
		t.Fatalf("dropped = %d, want the oversized history message", dropped)
	}
}

func TestTrimMessagesToBudgetLeavesWhatFitsAlone(t *testing.T) {
	msgs := []any{msg("user", "hi"), msg("assistant", "hello"), msg("user", "again")}
	req := map[string]any{"messages": msgs}

	_, dropped := trimMessagesToBudget(req, 10_000)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want nothing dropped when the body fits", dropped)
	}
	if len(req["messages"].([]any)) != len(msgs) {
		t.Fatal("messages were rebuilt even though nothing was over budget")
	}
}

func TestTrimMessagesToBudgetHandlesNoMessages(t *testing.T) {
	req := map[string]any{"messages": []any{}}

	left, dropped := trimMessagesToBudget(req, 100)

	if left != 0 || dropped != 0 {
		t.Fatalf("trim of an empty conversation = (%d, %d), want (0, 0)", left, dropped)
	}
}

func TestClampOutputBudgetLowersWhatTheClientSet(t *testing.T) {
	req := map[string]any{
		"max_tokens":            float64(4096),
		"max_completion_tokens": float64(100),
	}

	// 200 input chars estimate to 50 tokens, leaving 50 of a 100-token ceiling.
	if !clampOutputBudget(req, 100, 200) {
		t.Fatal("clampOutputBudget reported no change, want both fields lowered")
	}
	if got := req["max_tokens"].(float64); got != 50 {
		t.Fatalf("max_tokens = %v, want 50", got)
	}
	if got := req["max_completion_tokens"].(float64); got != 50 {
		t.Fatalf("max_completion_tokens = %v, want 50", got)
	}
}

// Inventing a field the client never sent would move the executor's own default
// output budget, which is not what a context ceiling is for.
func TestClampOutputBudgetNeverInventsAField(t *testing.T) {
	req := map[string]any{"temperature": float64(0.2)}

	if clampOutputBudget(req, 100, 200) {
		t.Fatal("clampOutputBudget reported a change with no output budget set")
	}
	if _, ok := req["max_tokens"]; ok {
		t.Fatal("clampOutputBudget invented max_tokens")
	}
}

func TestClampOutputBudgetFloorsAtOne(t *testing.T) {
	req := map[string]any{"max_tokens": float64(4096)}

	// Input already over the ceiling: the output budget cannot go below 1 or the
	// request would be nonsense.
	if !clampOutputBudget(req, 100, 4000) {
		t.Fatal("clampOutputBudget reported no change")
	}
	if got := req["max_tokens"].(float64); got != 1 {
		t.Fatalf("max_tokens = %v, want 1", got)
	}
}

func TestApplyModelContextLimitNoConfiguredCeiling(t *testing.T) {
	h, cleanup := setupHandlerForForward(t)
	defer cleanup()

	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a", 4000) + `"}]}`)

	if got := h.applyModelContextLimit("deepseek", "deepseek-chat", body); string(got) != string(body) {
		t.Fatal("a model with no configured ceiling must come back byte-identical")
	}
}

func TestApplyModelContextLimitTrimsAndClamps(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	if _, err := database.Exec(`UPDATE settings SET data = ? WHERE id = 1`,
		`{"modelContextLimit":{"deepseek/deepseek-chat":32}}`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	req := map[string]any{
		"max_tokens": float64(4096),
		"messages": []any{
			msg("user", strings.Repeat("a", 4000)),
			msg("assistant", strings.Repeat("b", 4000)),
			msg("user", "newest"),
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	out := h.applyModelContextLimit("deepseek", "deepseek-chat", body)

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("kept %d messages, want only the newest", len(msgs))
	}
	if content := msgs[0].(map[string]any)["content"]; content != "newest" {
		t.Fatalf("kept %v, want the newest message", content)
	}
	mt, ok := got["max_tokens"].(float64)
	if !ok {
		t.Fatal("max_tokens disappeared")
	}
	if mt >= 4096 || mt > 32 {
		t.Fatalf("max_tokens = %v, want it clamped into the 32-token ceiling", mt)
	}
}

// End to end through a combo: the operator sets the ceiling for a model in
// Combo & Routing, and the body that actually reaches the provider is the one
// that was trimmed to it.
func TestComboRequestHonoursModelContextLimit(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	var upstreamBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, err := io.ReadAll(r.Body); err == nil {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err == nil {
				upstreamBody.Store(body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"resp","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	// The shared fixture seeds a keyless deepseek connection; retire it so the
	// request can only land on the mock.
	if _, err := database.Exec(`UPDATE providerConnections SET isActive = 0 WHERE provider = 'deepseek'`); err != nil {
		t.Fatalf("retire seeded connection: %v", err)
	}
	connData, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": upstream.URL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-ctx', 'deepseek', 'apikey', 'Mock DeepSeek', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		string(connData)); err != nil {
		t.Fatalf("seed mock connection: %v", err)
	}

	// A combo whose only model is the one with a ceiling, set the way the
	// dashboard sets it.
	models, _ := json.Marshal([]string{"deepseek/deepseek-chat"})
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('combo-ctx', 'ctx-combo', 'llm', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		string(models)); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	settings, _ := json.Marshal(map[string]any{
		"comboStrategies":   map[string]string{"ctx-combo": "fallback"},
		"modelContextLimit": map[string]int{"deepseek/deepseek-chat": 32},
	})
	if _, err := database.Exec(`UPDATE settings SET data = ? WHERE id = 1`, string(settings)); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))

	reqBody, _ := json.Marshal(map[string]any{
		"model":      "ctx-combo",
		"max_tokens": 4096,
		"messages": []any{
			msg("user", strings.Repeat("a", 2000)),
			msg("assistant", strings.Repeat("b", 2000)),
			msg("user", "newest"),
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("combo request failed: %d %s", rec.Code, rec.Body.String())
	}

	raw := upstreamBody.Load()
	if raw == nil {
		t.Fatal("the mock upstream never saw a request")
	}
	sent := raw.(map[string]any)
	msgs, _ := sent["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("upstream got %d messages, want the history trimmed to the newest one", len(msgs))
	}
	if content := msgs[0].(map[string]any)["content"]; content != "newest" {
		t.Fatalf("upstream got %v, want the newest message", content)
	}
	if mt, ok := sent["max_tokens"].(float64); !ok || mt > 32 {
		t.Fatalf("upstream max_tokens = %v, want it clamped inside the 32-token ceiling", sent["max_tokens"])
	}
}
