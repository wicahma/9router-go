package chat

import (
	"testing"

	"9router/proxy/internal/translator"
)

func TestApplyThinkingLevelOpenAI(t *testing.T) {
	body := map[string]any{"model": "gpt-5.6-luna"}
	base, err := applyThinkingLevel(body, "openai", "gpt-5.6-luna(high)", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base != "gpt-5.6-luna" {
		t.Errorf("base = %q, want gpt-5.6-luna", base)
	}
	if body["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if body["model"] != "gpt-5.6-luna" {
		t.Errorf("model = %v, want bare id", body["model"])
	}
}

func TestApplyThinkingLevelClaudeAdaptive(t *testing.T) {
	body := map[string]any{"model": "claude-opus-5"}
	base, err := applyThinkingLevel(body, "anthropic", "claude-opus-5(max)", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base != "claude-opus-5" {
		t.Errorf("base = %q", base)
	}
	th, ok := body["thinking"].(map[string]any)
	if !ok || th["type"] != "adaptive" {
		t.Errorf("thinking = %v, want adaptive", body["thinking"])
	}
	oc, ok := body["output_config"].(map[string]any)
	if !ok || oc["effort"] != "high" {
		t.Errorf("output_config = %v, want effort high", body["output_config"])
	}
}

func TestApplyThinkingLevelClaudeBudget(t *testing.T) {
	body := map[string]any{"model": "claude-3-7-sonnet-latest"}
	base, err := applyThinkingLevel(body, "anthropic", "claude-3-7-sonnet-latest(high)", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base != "claude-3-7-sonnet-latest" {
		t.Errorf("base = %q", base)
	}
	th, ok := body["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Errorf("thinking = %v, want enabled", body["thinking"])
	}
	if th["budget_tokens"] != translator.LevelToBudget("high") {
		t.Errorf("budget_tokens = %v", th["budget_tokens"])
	}
}

func TestApplyThinkingLevelNoop(t *testing.T) {
	// default (no suffix) and (none)/(auto) all mean "do not send a level"
	for _, m := range []string{"gpt-5.6-luna", "gpt-5.6-luna(none)", "gpt-5.6-luna(auto)"} {
		body := map[string]any{}
		base, err := applyThinkingLevel(body, "openai", m, false)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", m, err)
		}
		if base != "gpt-5.6-luna" {
			t.Errorf("%s: base = %q", m, base)
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("%s: reasoning_effort set on noop", m)
		}
	}
}

func TestApplyThinkingLevelRejectsUnknown(t *testing.T) {
	// "max" is not in the openai level list
	if _, err := applyThinkingLevel(map[string]any{}, "openai", "gpt-5.6-luna(max)", false); err == nil {
		t.Fatal("expected error for unsupported level, got nil")
	}
}
