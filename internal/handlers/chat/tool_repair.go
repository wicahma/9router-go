package chat

import (
	json "encoding/json/v2"
	"fmt"
)

// repairToolCallIDsInMap ensures every role: "tool" message is paired with a
// matching tool_call on the assistant turn it answers (PR #4090). Strict
// upstreams (NVIDIA, OpenAI, Antigravity, Trust Is Gold) reject a history
// whose tool results have no corresponding call with "tool calls and tool
// results do not match", so minting a tool_call_id on the result is not
// enough: the assistant's tool_calls entry must exist too.
func repairToolCallIDsInMap(body map[string]any) {
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	var pending []string
	toolSeq := 0
	// assistant is the most recent assistant message; a result with no
	// pending call attaches to it.
	var assistant map[string]any
	out := make([]any, 0, len(msgs))
	for i, m := range msgs {
		msgMap, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)
			continue
		}
		role, _ := msgMap["role"].(string)
		switch role {
		case "assistant":
			if tcs, ok := msgMap["tool_calls"].([]any); ok && len(tcs) > 0 {
				for k, tcRaw := range tcs {
					if tcMap, ok := tcRaw.(map[string]any); ok {
						id, _ := tcMap["id"].(string)
						if id == "" {
							id = fmt.Sprintf("call_%d_%d", toolSeq, k)
							tcMap["id"] = id
						}
						pending = append(pending, id)
					}
				}
				toolSeq++
			}
			assistant = msgMap
			out = append(out, msgMap)
		case "tool":
			id, _ := msgMap["tool_call_id"].(string)
			if id != "" {
				for idx, p := range pending {
					if p == id {
						pending = append(pending[:idx], pending[idx+1:]...)
						break
					}
				}
				out = append(out, msgMap)
				continue
			}
			if len(pending) > 0 {
				msgMap["tool_call_id"] = pending[0]
				pending = pending[1:]
				out = append(out, msgMap)
				continue
			}
			if assistant == nil {
				assistant = map[string]any{"role": "assistant", "content": ""}
				out = append(out, assistant)
			}
			id = fmt.Sprintf("call_tool_%d", i)
			msgMap["tool_call_id"] = id
			appendToolCall(assistant, id)
			out = append(out, msgMap)
		default:
			out = append(out, msgMap)
		}
	}
	body["messages"] = out
}

// appendToolCall adds a synthetic function call carrying id to an assistant
// message, creating the tool_calls array on first use. The name and arguments
// are opaque: strict upstreams only check that a call and its result line up.
func appendToolCall(assistant map[string]any, id string) {
	tcs, _ := assistant["tool_calls"].([]any)
	tcs = append(tcs, map[string]any{
		"id":       id,
		"type":     "function",
		"function": map[string]any{"name": "tool", "arguments": "{}"},
	})
	assistant["tool_calls"] = tcs
}

// repairToolCallIDsInJSON repairs missing tool_call_id directly on raw JSON body if it contains messages.
func repairToolCallIDsInJSON(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	if _, ok := m["messages"]; ok {
		repairToolCallIDsInMap(m)
		if out, err := json.Marshal(m); err == nil {
			return out
		}
	}
	return body
}
