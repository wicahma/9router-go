package chat

import (
	json "encoding/json/v2"
	"testing"

	"9router/proxy/internal/translator"
)

func TestBuildUsageMeta_CarriesLatencyAndTokens(t *testing.T) {
	info := &UsageLogInfo{Provider: "kiro", Model: "claude-sonnet-4", ConnectionID: "conn-7"}
	usage := &translator.OpenAIUsage{PromptTokens: 120, CompletionTokens: 45}

	raw := buildUsageMeta(info, 1234, 87, 200, usage, 30, 5)

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
}

func TestBuildUsageMeta_NonStreamingHasNoTTFT(t *testing.T) {
	info := &UsageLogInfo{Provider: "agnes", Model: "gpt-4o", ConnectionID: "c1"}
	usage := &translator.OpenAIUsage{PromptTokens: 5, CompletionTokens: 5}

	var got usageMeta
	if err := json.Unmarshal(buildUsageMeta(info, 300, 0, 200, usage, 0, 0), &got); err != nil {
		t.Fatal(err)
	}
	if got.Streamed {
		t.Error("a zero TTFT means no stream, so streamed must be false")
	}
	if got.LatencyMs != 300 {
		t.Errorf("latencyMs = %d, want 300", got.LatencyMs)
	}
}
