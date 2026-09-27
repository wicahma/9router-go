package chat

import (
	json "encoding/json/v2"

	"9router/proxy/internal/pricing"
	"9router/proxy/internal/translator"
)

// usageMeta is the JSON document persisted in usageHistory.meta.
//
// The column existed from the start but only ever carried a copy of three
// columns the row already has, so the interesting measurements — how long the
// request took, and how long until the first streamed token — were computed on
// every request, written to the log file, and then dropped. They are persisted
// here so latency can be queried instead of guessed at.
type usageMeta struct {
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	ConnectionID  string  `json:"connectionId"`
	LatencyMs     int64   `json:"latencyMs"`
	TTFTMs        int64   `json:"ttftMs"`
	HTTPStatus    int     `json:"httpStatus"`
	Streamed      bool    `json:"streamed"`
	CostSource    string  `json:"costSource"`
	Cost          float64 `json:"cost"`
	PromptTokens  int     `json:"promptTokens"`
	OutputTokens  int     `json:"completionTokens"`
	CachedTokens  int     `json:"cachedTokens"`
	CacheCreation int     `json:"cacheCreationInputTokens"`
	// Attempts counts upstream forwards burned by this client request. One means
	// the first connection worked; higher means fallback/combo retries. Without
	// it a slow success is indistinguishable from one that barely survived.
	Attempts int `json:"attempts"`
}

// attemptsFor reports how many upstream tries the request cost, floored at one
// so an old caller that never counted is not reported as zero tries.
func attemptsFor(info *UsageLogInfo) int {
	if info == nil || info.Attempts < 1 {
		return 1
	}
	return info.Attempts
}

// buildUsageMeta assembles the meta document for a completed request. A
// non-streaming exchange has ttftMs of 0, which is why Streamed is derived from
// it rather than passed in.
func buildUsageMeta(info *UsageLogInfo, latencyMs, ttftMs int64, httpStatus int, usage *translator.OpenAIUsage, cached, cacheCreation int, cost float64, costSource pricing.Source) []byte {
	attempts := attemptsFor(info)
	b, err := json.Marshal(usageMeta{
		Provider:      info.Provider,
		Model:         info.Model,
		ConnectionID:  info.ConnectionID,
		LatencyMs:     latencyMs,
		TTFTMs:        ttftMs,
		HTTPStatus:    httpStatus,
		Streamed:      ttftMs > 0,
		CostSource:    string(costSource),
		Cost:          cost,
		PromptTokens:  usage.PromptTokens,
		OutputTokens:  usage.CompletionTokens,
		CachedTokens:  cached,
		CacheCreation: cacheCreation,
		Attempts:      attempts,
	})
	if err != nil {
		// A struct of scalars cannot fail to marshal; keep the row anyway.
		return []byte(`{"provider":"` + info.Provider + `","model":"` + info.Model + `","connectionId":"` + info.ConnectionID + `"}`)
	}
	return b
}
