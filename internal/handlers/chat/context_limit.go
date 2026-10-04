package chat

import (
	json "encoding/json/v2"

	"9router/proxy/internal/log"
)

// contextLimitCharsPerToken is the 4-chars-per-token approximation the rest of
// the gateway already uses (estimateAnthropicTokens, the usage counters), so a
// ceiling typed into the dashboard counts the same tokens the estimator
// reports. Upgrade path: a real tokenizer behind the same helpers.
const contextLimitCharsPerToken = 4

// applyModelContextLimit trims a request body to the context ceiling an
// operator set for this model in Combo & Routing (settings.modelContextLimit,
// keyed "provider/model"), so a model whose real window is smaller than the
// catalog claims is never handed more history than it can hold.
//
// Only the input side is bounded: the newest messages are kept, the oldest
// droppable ones are dropped, and an output budget the client already set is
// lowered so input+output cannot exceed the ceiling. A model with no configured
// limit, or a body that already fits, comes back byte-identical.
func (h *ChatHandler) applyModelContextLimit(provider, model string, body []byte) []byte {
	if h == nil || h.Repo == nil || len(body) == 0 {
		return body
	}
	limit := h.modelContextLimit(provider, model)
	if limit <= 0 {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	if _, ok := req["messages"].([]any); !ok {
		return body
	}

	keptChars, dropped := trimMessagesToBudget(req, limit*contextLimitCharsPerToken)
	clamped := clampOutputBudget(req, limit, keptChars)
	if dropped == 0 && !clamped {
		return body
	}

	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	if dropped > 0 {
		log.Info("context limit trimmed history", "model", provider+"/"+model,
			"limit_tokens", limit, "dropped_messages", dropped)
	}
	return out
}

// modelContextLimit reads the configured ceiling for "provider/model".
func (h *ChatHandler) modelContextLimit(provider, model string) int {
	settings, err := h.Repo.GetSettings()
	if err != nil || settings == nil {
		return 0
	}
	return settings.ModelContextLimit[provider+"/"+model]
}

// trimMessagesToBudget drops the oldest droppable messages until what is left
// fits budget chars, and reports how many it dropped plus the char count of
// what is left (for the output clamp). System/developer turns and the final
// message are never dropped: the first carry the instructions the request is
// meaningless without, the last is the question being asked.
func trimMessagesToBudget(req map[string]any, budget int) (left int, dropped int) {
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return 0, 0
	}

	fixed := 0
	for _, key := range []string{"system", "tools", "tool_choice", "functions"} {
		if v, ok := req[key]; ok {
			fixed += countValueChars(v)
		}
	}

	protected := func(i int) bool {
		if i == len(msgs)-1 {
			return true
		}
		role, _ := msgs[i].(map[string]any)["role"].(string)
		return role == "system" || role == "developer"
	}

	// Walk newest to oldest and let the budget decide where the history is
	// cut, so the newest turns always survive and the oldest go first — which
	// is what "this model's window is smaller than this conversation" means.
	keep := make([]bool, len(msgs))
	used := fixed
	for i := len(msgs) - 1; i >= 0; i-- {
		size := countValueChars(msgs[i])
		if protected(i) {
			keep[i] = true
			used += size
			continue
		}
		if used+size > budget {
			continue
		}
		keep[i] = true
		used += size
	}

	out := make([]any, 0, len(msgs))
	for i, m := range msgs {
		if keep[i] {
			out = append(out, m)
			continue
		}
		dropped++
	}
	if dropped == 0 {
		return used, 0
	}
	if len(out) == 0 {
		// Every message was over budget on its own; keep the newest rather
		// than send an empty conversation.
		out = append(out, msgs[len(msgs)-1])
	}
	req["messages"] = out
	return used, dropped
}

// clampOutputBudget lowers an output budget the client already set so
// input+output cannot exceed the ceiling. A field the client never sent is left
// alone: inventing one would move the executor's default output budget, which
// is not what a context ceiling is for. Reports whether it changed anything.
func clampOutputBudget(req map[string]any, limit, inputChars int) bool {
	remaining := limit - (inputChars+contextLimitCharsPerToken-1)/contextLimitCharsPerToken
	if remaining < 1 {
		remaining = 1
	}
	changed := false
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		f, ok := req[key].(float64)
		if !ok || int(f) <= remaining {
			continue
		}
		req[key] = float64(remaining)
		changed = true
	}
	return changed
}
