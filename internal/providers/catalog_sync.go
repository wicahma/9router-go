package providers

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"9router/proxy/internal/log"
)

const (
	ModelsDevCatalogURL = "https://models.dev/api.json"
	CatalogSyncInterval = 24 * time.Hour
)

// SyncedModelModalities holds extracted capabilities from models.dev for a single model ID.
type SyncedModelModalities struct {
	Vision     bool `json:"vision"`
	PDF        bool `json:"pdf"`
	AudioInput bool `json:"audioInput"`
	VideoInput bool `json:"videoInput"`
}

// SyncedModelLimits holds token limits for a provider/model pair.
type SyncedModelLimits struct {
	ContextWindow int `json:"contextWindow,omitempty"`
	MaxOutput     int `json:"maxOutput,omitempty"`
}

// SyncedModelPrice holds the consensus upstream price for a model, in USD per
// million tokens.
//
// models.dev lists a model once per provider, and those entries disagree:
// kimi-k3 is served by 32 providers carrying 13 different prices. A single
// model id therefore has no single price, so the value kept here is the one
// most providers agree on, and Agreement records how strong that consensus was.
type SyncedModelPrice struct {
	InputPer1M  float64 `json:"inputPer1M"`
	OutputPer1M float64 `json:"outputPer1M"`
	// Agreement is the fraction of priced providers that quoted this exact
	// pair. A value below 0.5 means no majority existed and nothing is stored.
	Agreement float64 `json:"agreement"`
}

// SyncedCatalog represents the processed catalog file written to disk / kept in memory.
// The file is shared with upstream, whose writer emits `syncedAt` as epoch
// milliseconds and adds `v`/`etag` fields — so SyncedAt is decoded leniently.
type SyncedCatalog struct {
	Version   int                                     `json:"v,omitempty"`
	ETag      string                                  `json:"etag,omitempty"`
	SyncedAt  string                                  `json:"-"`
	Models    map[string]SyncedModelModalities        `json:"models"`
	Providers map[string]map[string]SyncedModelLimits `json:"providers"`
	// Prices maps a base model id to its consensus upstream price. Only models
	// with a clear majority are present, so a miss is honest rather than a
	// silent fallback to an invented rate.
	Prices map[string]SyncedModelPrice `json:"prices,omitempty"`
}

// UnmarshalJSON accepts syncedAt as either an ISO string or epoch milliseconds.
func (c *SyncedCatalog) UnmarshalJSON(data []byte) error {
	type alias SyncedCatalog
	aux := struct {
		SyncedAt []byte `json:"syncedAt"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.SyncedAt) == 0 || string(aux.SyncedAt) == "null" {
		c.SyncedAt = ""
		return nil
	}
	if err := json.Unmarshal(aux.SyncedAt, &c.SyncedAt); err == nil {
		return nil
	}
	var epochMS float64
	if err := json.Unmarshal(aux.SyncedAt, &epochMS); err != nil {
		return fmt.Errorf("catalog syncedAt: %w", err)
	}
	c.SyncedAt = time.UnixMilli(int64(epochMS)).UTC().Format(time.RFC3339)
	return nil
}

type CatalogSyncState struct {
	Running    bool   `json:"running"`
	LastSync   string `json:"lastSync,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	ETag       string `json:"etag,omitempty"`
	ModelCount int    `json:"modelCount"`
}

var (
	catalogMu     sync.RWMutex
	globalCatalog *SyncedCatalog
	syncState     CatalogSyncState
	syncStateMu   sync.Mutex
)

// ProviderAliases maps 9router provider IDs to models.dev provider IDs for limit resolution.
var ProviderAliases = map[string]string{
	"glm":           "zai",
	"glm-cn":        "zhipuai",
	"claude":        "anthropic",
	"gemini":        "google",
	"kimi":          "moonshotai",
	"kimi-cn":       "moonshotai-cn",
	"qwen":          "alibaba",
	"qwen-cn":       "alibaba-cn",
	"zhipu":         "zhipuai",
	"hunyuan":       "tencent",
	"doubao":        "volcengine",
	"cloudflare-ai": "cloudflare-workers-ai",
}

// GetCatalogState returns the current synchronization state.
func GetCatalogState() CatalogSyncState {
	syncStateMu.Lock()
	defer syncStateMu.Unlock()
	return syncState
}

// GetCatalogModalities looks up dynamically synced modalities for a model ID.
func GetCatalogModalities(model string) *SyncedModelModalities {
	if model == "" {
		return nil
	}
	base := baseModelID(model)

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if globalCatalog == nil || globalCatalog.Models == nil {
		return nil
	}
	if m, ok := globalCatalog.Models[base]; ok {
		return &m
	}
	return nil
}

// baseModelID reduces an upstream model id to the bare model name. Ids are not
// all one segment deep ("accounts/fireworks/models/x"), so everything up to the
// last slash is dropped, as is a trailing ":tag".
//
// The catalog keys and the lookups must agree on this, otherwise a bare name
// can never hit a price stored under a nested key.
func baseModelID(model string) string {
	base := strings.ToLower(model)
	if idx := strings.LastIndex(base, "/"); idx != -1 {
		base = base[idx+1:]
	}
	if idx := strings.Index(base, ":"); idx != -1 {
		base = base[:idx]
	}
	return base
}

// GetCatalogPrice returns the consensus upstream price for a model, or false
// when the catalog has no clear majority for it.
func GetCatalogPrice(model string) (SyncedModelPrice, bool) {
	if model == "" {
		return SyncedModelPrice{}, false
	}
	base := baseModelID(model)

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if globalCatalog == nil {
		return SyncedModelPrice{}, false
	}
	if p, ok := globalCatalog.Prices[base]; ok {
		return p, true
	}

	// No canonical entry: fall back to a provider alias of the same model,
	// strongest agreement first. Scanning is safe only as a miss path — a bare
	// key always wins above — and picking by agreement keeps the result from
	// depending on Go's map order.
	var best SyncedModelPrice
	found := false
	for key, p := range globalCatalog.Prices {
		if baseModelID(key) != base {
			continue
		}
		if !found || p.Agreement > best.Agreement {
			best, found = p, true
		}
	}
	return best, found
}

// GetCatalogLimits looks up the models.dev-synced token limits for a
// provider/model pair. Provider ids are mapped through ProviderAliases first
// (our `claude` is models.dev `anthropic`, and so on), and the bare model id is
// accepted as a fallback key. This is the same per-provider+model source
// upstream getCapabilitiesForModel consults, so token limits stay in sync with
// the catalog instead of relying on substring heuristics.
func GetCatalogLimits(provider, model string) (contextWindow, maxOutput int) {
	if model == "" {
		return 0, 0
	}
	base := strings.ToLower(model)
	if idx := strings.Index(base, "/"); idx != -1 {
		base = base[idx+1:]
	}
	if idx := strings.Index(base, ":"); idx != -1 {
		base = base[:idx]
	}

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if globalCatalog == nil || globalCatalog.Providers == nil {
		return 0, 0
	}
	keys := []string{strings.ToLower(provider)}
	if mapped, ok := ProviderAliases[strings.ToLower(provider)]; ok {
		keys = append(keys, mapped)
	}
	for _, key := range keys {
		byModel, ok := globalCatalog.Providers[key]
		if !ok {
			continue
		}
		if limits, ok := byModel[base]; ok {
			return limits.ContextWindow, limits.MaxOutput
		}
	}
	return 0, 0
}

// LoadCatalogFromFile loads cached catalog from disk if it exists.
func LoadCatalogFromFile(filePath string) error {
	if filePath == "" {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var cat SyncedCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return err
	}

	catalogMu.Lock()
	globalCatalog = &cat
	catalogMu.Unlock()

	syncStateMu.Lock()
	syncState.LastSync = cat.SyncedAt
	syncState.ModelCount = len(cat.Models)
	syncStateMu.Unlock()

	return nil
}

// SyncModelCatalog performs a download and parsing pass of models.dev API catalog.
func SyncModelCatalog(ctx context.Context, client *http.Client, filePath string) error {
	syncStateMu.Lock()
	if syncState.Running {
		syncStateMu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	syncState.Running = true
	syncStateMu.Unlock()

	defer func() {
		syncStateMu.Lock()
		syncState.Running = false
		syncStateMu.Unlock()
	}()

	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsDevCatalogURL, nil)
	if err != nil {
		return fmt.Errorf("create catalog request: %w", err)
	}

	syncStateMu.Lock()
	etag := syncState.ETag
	syncStateMu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := client.Do(req)
	if err != nil {
		syncStateMu.Lock()
		syncState.LastError = err.Error()
		syncStateMu.Unlock()
		return fmt.Errorf("catalog request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		log.Info("catalog_sync", "catalog not modified (304)")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		errText := fmt.Sprintf("catalog sync non-200 status: %d", resp.StatusCode)
		syncStateMu.Lock()
		syncState.LastError = errText
		syncStateMu.Unlock()
		return errors.New(errText)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50MB limit
	if err != nil {
		return fmt.Errorf("read catalog body: %w", err)
	}

	// models.dev format: map of providerID -> providerData { models: map[modelID]modelData }
	var rawData map[string]struct {
		Models map[string]struct {
			Modality map[string]bool `json:"modality"`
			Limit    *struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
			Cost *struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
			} `json:"cost"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &rawData); err != nil {
		syncStateMu.Lock()
		syncState.LastError = err.Error()
		syncStateMu.Unlock()
		return fmt.Errorf("decode catalog json: %w", err)
	}

	modelsMap := make(map[string]SyncedModelModalities)
	providersMap := make(map[string]map[string]SyncedModelLimits)
	// priceVotes counts, per base model id, how many providers quoted each
	// distinct price pair.
	priceVotes := make(map[string]map[[2]float64]int)

	for provID, provData := range rawData {
		for modelID, mData := range provData.Models {
			base := baseModelID(modelID)

			// Aggregate modalities
			cur := modelsMap[base]
			if mData.Modality["image"] || mData.Modality["vision"] {
				cur.Vision = true
			}
			if mData.Modality["pdf"] {
				cur.PDF = true
			}
			if mData.Modality["audio"] {
				cur.AudioInput = true
			}
			if mData.Modality["video"] {
				cur.VideoInput = true
			}
			modelsMap[base] = cur

			// Limits
			if mData.Limit != nil && (mData.Limit.Context > 0 || mData.Limit.Output > 0) {
				if providersMap[provID] == nil {
					providersMap[provID] = make(map[string]SyncedModelLimits)
				}
				providersMap[provID][base] = SyncedModelLimits{
					ContextWindow: mData.Limit.Context,
					MaxOutput:     mData.Limit.Output,
				}
			}

			// Prices
			if mData.Cost != nil {
				pair := [2]float64{mData.Cost.Input, mData.Cost.Output}
				if priceVotes[base] == nil {
					priceVotes[base] = make(map[[2]float64]int)
				}
				priceVotes[base][pair]++
			}
		}
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	synced := &SyncedCatalog{
		SyncedAt:  nowStr,
		Models:    modelsMap,
		Providers: providersMap,
		Prices:    consensusPrices(priceVotes),
	}

	catalogMu.Lock()
	globalCatalog = synced
	catalogMu.Unlock()
	InvalidateCapabilitiesCache()

	newEtag := resp.Header.Get("ETag")
	syncStateMu.Lock()
	syncState.LastSync = nowStr
	syncState.LastError = ""
	syncState.ETag = newEtag
	syncState.ModelCount = len(modelsMap)
	syncStateMu.Unlock()

	// Write to disk if filePath configured
	if filePath != "" {
		_ = os.MkdirAll(filepath.Dir(filePath), 0755)
		if outBytes, err := json.Marshal(synced, jsontext.WithIndent("  ")); err == nil {
			_ = os.WriteFile(filePath, outBytes, 0644)
		}
	}

	log.Info("catalog_sync", "catalog synchronized successfully", "models", len(modelsMap), "providers", len(providersMap))
	return nil
}

// consensusPrices reduces per-provider price quotes to one rate per model.
//
// A model only gets an entry when strictly more than half of the providers
// quoting it agree, so an outlier reseller cannot set the rate and a genuine
// tie leaves the model unpriced instead of resolving on Go's random map
// iteration order.
//
// A (0,0) quote is a subscription or token plan with no marginal per-token
// charge. Such providers are excluded from the vote: they would otherwise form
// a large majority on models that are free under a plan and drag the rate to
// zero for everyone paying per token.
func consensusPrices(votes map[string]map[[2]float64]int) map[string]SyncedModelPrice {
	out := make(map[string]SyncedModelPrice, len(votes))
	for model, tally := range votes {
		var total, best int
		var winner [2]float64
		for pair, n := range tally {
			if pair == ([2]float64{0, 0}) {
				continue
			}
			total += n
			if n > best {
				best, winner = n, pair
			}
		}
		if total == 0 || 2*best <= total {
			continue
		}
		out[model] = SyncedModelPrice{
			InputPer1M:  winner[0],
			OutputPer1M: winner[1],
			Agreement:   float64(best) / float64(total),
		}
	}
	return out
}

// StartBackgroundCatalogSync runs initial sync and 24h timer loop.
func StartBackgroundCatalogSync(ctx context.Context, client *http.Client, filePath string) {
	_ = LoadCatalogFromFile(filePath)

	go func() {
		// Wait 30s after boot for first sync
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		if err := SyncModelCatalog(ctx, client, filePath); err != nil {
			log.Warn("catalog_sync", "initial sync failed", "error", err)
		}

		ticker := time.NewTicker(CatalogSyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := SyncModelCatalog(ctx, client, filePath); err != nil {
					log.Warn("catalog_sync", "periodic sync failed", "error", err)
				}
			}
		}
	}()
}
