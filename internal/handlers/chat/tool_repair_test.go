package chat

import "testing"

func TestRepairToolCallIDsInMap(t *testing.T) {
	t.Run("pairs missing tool_call_id with assistant tool call", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "call_123", "type": "function", "function": map[string]any{"name": "bash"}},
					},
				},
				map[string]any{
					"role":    "tool",
					"content": "output text",
					// tool_call_id is missing!
				},
			},
		}

		repairToolCallIDsInMap(body)

		msgs := body["messages"].([]any)
		toolMsg := msgs[1].(map[string]any)
		if toolMsg["tool_call_id"] != "call_123" {
			t.Errorf("expected tool_call_id 'call_123', got %v", toolMsg["tool_call_id"])
		}
	})

	t.Run("preserves existing tool_call_id when already matching", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "call_existing", "type": "function", "function": map[string]any{"name": "bash"}},
					},
				},
				map[string]any{
					"role":         "tool",
					"tool_call_id": "call_existing",
					"content":      "output text",
				},
			},
		}

		repairToolCallIDsInMap(body)

		msgs := body["messages"].([]any)
		toolMsg := msgs[1].(map[string]any)
		if toolMsg["tool_call_id"] != "call_existing" {
			t.Errorf("expected 'call_existing', got %v", toolMsg["tool_call_id"])
		}
	})

	t.Run("mints fallback id and declares the assistant call for an orphan tool message", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role":    "tool",
					"content": "orphan output",
				},
			},
		}

		repairToolCallIDsInMap(body)

		msgs := body["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages (assistant + tool), got %d", len(msgs))
		}
		asst, ok := msgs[0].(map[string]any)
		if !ok || asst["role"] != "assistant" {
			t.Fatalf("expected synthesised assistant first, got %v", msgs[0])
		}
		toolMsg := msgs[1].(map[string]any)
		id, ok := toolMsg["tool_call_id"].(string)
		if !ok || id == "" {
			t.Fatalf("expected minted tool_call_id, got %v", toolMsg["tool_call_id"])
		}
		tcs, ok := asst["tool_calls"].([]any)
		if !ok || len(tcs) != 1 {
			t.Fatalf("expected 1 declared tool_call on the assistant, got %v", asst["tool_calls"])
		}
		tc := tcs[0].(map[string]any)
		if tc["id"] != id {
			t.Errorf("assistant tool_call id %v does not match tool_call_id %v", tc["id"], id)
		}
	})

	t.Run("attaches a missing tool_call_id to the preceding assistant and declares the call", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "assistant", "content": ""},
				map[string]any{"role": "tool", "content": "result one"},
				map[string]any{"role": "tool", "content": "result two"},
			},
		}

		repairToolCallIDsInMap(body)

		msgs := body["messages"].([]any)
		asst := msgs[0].(map[string]any)
		tcs, ok := asst["tool_calls"].([]any)
		if !ok || len(tcs) != 2 {
			t.Fatalf("expected 2 declared tool_calls, got %v", asst["tool_calls"])
		}
		for i, want := range []string{"result one", "result two"} {
			toolMsg := msgs[i+1].(map[string]any)
			if toolMsg["content"] != want {
				t.Errorf("message %d content = %v, want %v", i+1, toolMsg["content"], want)
			}
			id, _ := toolMsg["tool_call_id"].(string)
			if id == "" {
				t.Errorf("message %d has no tool_call_id", i+1)
			}
			tc := tcs[i].(map[string]any)
			if tc["id"] != id {
				t.Errorf("message %d tool_call_id %v does not match declared call %v", i+1, id, tc["id"])
			}
		}
	})
}
