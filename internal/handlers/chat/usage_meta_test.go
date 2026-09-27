package chat

import (
	json "encoding/json/v2"
	"testing"

	"9router/proxy/internal/pricing"
	"9router/proxy/internal/translator"
)

func TestBuildUsageMeta_CarriesLatencyAndTokens(t *testing.T) {
	info := &UsageLogInfo{Provider: "kiro", Model: "claude-sonnet-4", ConnectionID: "conn-7"}
	usage := &translator.OpenAIUsage{PromptTokens: 120, CompletionTokens: 45}

	raw := buildUsageMeta(info, 1234, 87, 200, usage, 30, 5, 0.42, pricing.SourceTable)

	var got usageMeta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("meta is not valid JSON: %v (%s)", err, raw)
	}
	if got.LatencyMs != 1234 || got.TTFTMs != 87 {
		t.Errorf("latency not persisted: latencyMs=%d ttftMs=%d", got.LatencyMs, got.TTFTMs)
	}
	if got.HTTPStatus != 200 {
		t.Errorf("httpStatus = %d, want 200", got.HTTPStatus)
	}
	if !got.Streamed {
		t.Error("streamed must be true when ttftMs is set")
	}
	if got.PromptTokens != 120 || got.OutputTokens != 45 {
		t.Errorf("token counts not persisted: %+v", got)
	}
	if got.CachedTokens != 30 || got.CacheCreation != 5 {
		t.Errorf("cache counters not persisted: %+v", got)
	}
	if got.Provider != "kiro" || got.ConnectionID != "conn-7" {
		t.Errorf("identity fields not preserved: %+v", got)
	}
	if got.CostSource != "table" || got.Cost != 0.42 {
		t.Errorf("cost provenance not persisted: source=%q cost=%v", got.CostSource, got.Cost)
	}
}

func TestBuildUsageMeta_DefaultSourceIsNotDressedUpAsPriced(t *testing.T) {
	info := &UsageLogInfo{Provider: "cbcn", Model: "kimi-k3", ConnectionID: "c2"}
	usage := &translator.OpenAIUsage{PromptTokens: 10, CompletionTokens: 4}

	var got usageMeta
	raw := buildUsageMeta(info, 500, 0, 200, usage, 0, 0, 0.0004, pricing.SourceDefault)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.CostSource != "default" {
		t.Errorf("costSource = %q, want %q — an unpriced model must be visible as such", got.CostSource, pricing.SourceDefault)
	}
}

func TestBuildUsageMeta_NonStreamingHasNoTTFT(t *testing.T) {
	info := &UsageLogInfo{Provider: "agnes", Model: "gpt-4o", ConnectionID: "c1"}
	usage := &translator.OpenAIUsage{PromptTokens: 5, CompletionTokens: 5}

	var got usageMeta
	if err := json.Unmarshal(buildUsageMeta(info, 300, 0, 200, usage, 0, 0, 0.01, pricing.SourceDefault), &got); err != nil {
		t.Fatal(err)
	}
	if got.Streamed {
		t.Error("a zero TTFT means no stream, so streamed must be false")
	}
	if got.LatencyMs != 300 {
		t.Errorf("latencyMs = %d, want 300", got.LatencyMs)
	}
}

func TestBuildUsageMeta_AttemptsArePersisted(t *testing.T) {
	info := &UsageLogInfo{Provider: "kiro", Model: "claude-sonnet-4", ConnectionID: "c9", Attempts: 3}
	usage := &translator.OpenAIUsage{PromptTokens: 10, CompletionTokens: 5}

	var got usageMeta
	if err := json.Unmarshal(buildUsageMeta(info, 900, 0, 200, usage, 0, 0, 0.01, pricing.SourceTable), &got); err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 3 {
		t.Errorf("attempts = %d, want 3 — retries are the whole point of the field", got.Attempts)
	}
}

func TestBuildUsageMeta_UncountedRequestIsOneAttempt(t *testing.T) {
	// The media paths do not install a counter. Reporting 0 tries would read as
	// "never reached upstream", which is a different fact.
	info := &UsageLogInfo{Provider: "openai", Model: "gpt-4o", ConnectionID: "c1"}
	usage := &translator.OpenAIUsage{PromptTokens: 4, CompletionTokens: 4}

	var got usageMeta
	if err := json.Unmarshal(buildUsageMeta(info, 100, 0, 200, usage, 0, 0, 0.01, pricing.SourceDefault), &got); err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", got.Attempts)
	}
}
