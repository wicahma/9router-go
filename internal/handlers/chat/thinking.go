package chat

import (
	"fmt"
	"slices"
	"strings"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/translator"
)

// splitThinkingLevel splits the "(level)" suffix the dashboard appends to a
// copied model id, e.g. "claude-opus-5(max)". The suffix is a request override:
// it never reaches the upstream model name.
func splitThinkingLevel(model string) (base, level string, ok bool) {
	i := strings.LastIndexByte(model, '(')
	if i <= 0 || !strings.HasSuffix(model, ")") {
		return model, "", false
	}
	return model[:i], model[i+1 : len(model)-1], true
}

// stripThinkingLevel returns the model id without its level suffix.
func stripThinkingLevel(model string) string {
	base, _, _ := splitThinkingLevel(model)
	return base
}

// applyThinkingLevel reads the "(level)" suffix off model, validates it against
// the provider's capability table, and writes the matching thinking control into
// body. It always returns the bare model id.
//
// No suffix, "(auto)" and "(none)" are all no-ops: the level is simply not sent,
// which is the dashboard's default. A suffix the model does not support is an
// error rather than a silent drop, so a stale copied id fails loudly.
// claudeWire selects the Anthropic request shape (thinking/output_config)
// instead of OpenAI's reasoning_effort.
func applyThinkingLevel(body map[string]any, provider, model string, claudeWire bool) (string, error) {
	base, level, ok := splitThinkingLevel(model)
	if !ok || level == "" || level == "auto" || level == "none" {
		return base, nil
	}

	if !slices.Contains(providers.GetThinkingLevels(provider, base), level) {
		return base, fmt.Errorf("model %q does not support thinking level %q", base, level)
	}

	format := providers.GetCapabilitiesForModel(provider, base).ThinkingFormat
	effort, ok := translator.LevelToEffort(format, level)
	if !ok {
		return base, fmt.Errorf("thinking level %q is not supported on %s models", level, format)
	}

	delete(body, "thinking")
	if claudeWire {
		switch format {
		case "claude-budget":
			body["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": translator.LevelToBudget(level),
			}
			return base, nil
		case "claude-adaptive":
			body["thinking"] = map[string]any{"type": "adaptive"}
			body["output_config"] = map[string]any{"effort": effort}
			return base, nil
		}
	}

	body["reasoning_effort"] = effort
	return base, nil
}
