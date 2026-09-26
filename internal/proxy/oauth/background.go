package oauth

import (
	"context"
	json "encoding/json/v2"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
)

// Refresh when expiry is within 30 minutes of now.
const (
	backgroundRefreshLead     = 30 * time.Minute
	backgroundRefreshInterval = 5 * time.Minute
	backgroundInitialDelay    = 10 * time.Second
	backgroundNormalDelay     = 1500 * time.Millisecond
	backgroundSensitiveDelay  = 12 * time.Second
	// defaultBackgroundExpiresIn covers a token endpoint that omits expiresIn:
	// ExpiresIn=0 would stamp expiresAt=now and re-select this connection on
	// every tick, refreshing in a loop.
	defaultBackgroundExpiresIn = 3600
)

// sensitiveProviders get a longer gap between refreshes so a burst of Google
// accounts cannot hammer the shared token endpoint.
var sensitiveProviders = map[string]bool{
	"antigravity": true,
	"gemini-cli":  true,
}

var (
	bgMu      sync.Mutex
	bgStarted bool
)

// ConnectionNeedingRefresh is one active OAuth connection whose access token
// expires inside the lead window.
type ConnectionNeedingRefresh struct {
	ID                   string
	Provider             string
	RefreshToken         string
	AccessToken          string
	ProviderSpecificData map[string]string
}

// SelectConnectionsNeedingRefresh is the pure selection step, exported for
// tests. Connections without a parsable expiry are skipped: refreshing blind
// risks churning a token endpoint for a token that never expires.
func SelectConnectionsNeedingRefresh(conns []*models.ProviderConnection, now time.Time) []ConnectionNeedingRefresh {
	var out []ConnectionNeedingRefresh
	for _, c := range conns {
		if c == nil || !strings.EqualFold(strings.ReplaceAll(c.AuthType, "_", ""), "oauth") {
			continue
		}
		due, ok := connectionDue(c, now)
		if ok {
			out = append(out, due)
		}
	}
	return out
}

func connectionDue(c *models.ProviderConnection, now time.Time) (ConnectionNeedingRefresh, bool) {
	var d struct {
		AccessToken          string         `json:"accessToken"`
		RefreshToken         string         `json:"refreshToken"`
		ExpiresAt            string         `json:"expiresAt"`
		ProviderSpecificData map[string]any `json:"providerSpecificData"`
	}
	if c.Data != "" {
		if err := json.Unmarshal([]byte(c.Data), &d); err != nil {
			return ConnectionNeedingRefresh{}, false
		}
	}
	if strings.TrimSpace(d.RefreshToken) == "" {
		return ConnectionNeedingRefresh{}, false
	}
	exp, err := time.Parse(time.RFC3339, strings.TrimSpace(d.ExpiresAt))
	if err != nil || exp.Sub(now) >= backgroundRefreshLead {
		return ConnectionNeedingRefresh{}, false
	}
	return ConnectionNeedingRefresh{
		ID:                   c.ID,
		Provider:             c.Provider,
		RefreshToken:         d.RefreshToken,
		AccessToken:          d.AccessToken,
		ProviderSpecificData: StringMap(d.ProviderSpecificData),
	}, true
}

// StartBackgroundRefresh launches the proactive OAuth token refresh loop.
// Safe to call multiple times; per-connection failures are logged, never fatal.
func StartBackgroundRefresh(ctx context.Context, repo *db.Repo) {
	if repo == nil {
		return
	}
	bgMu.Lock()
	if bgStarted {
		bgMu.Unlock()
		return
	}
	bgStarted = true
	bgMu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backgroundInitialDelay):
		}

		ticker := time.NewTicker(backgroundRefreshInterval)
		defer ticker.Stop()

		runBackgroundRefreshTick(ctx, repo)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runBackgroundRefreshTick(ctx, repo)
			}
		}
	}()
}

// runBackgroundRefreshTick refreshes every due connection, sequentially, with
// an inter-account delay so a multi-account burst stays under provider limits.
func runBackgroundRefreshTick(ctx context.Context, repo *db.Repo) {
	conns, err := repo.GetProviderConnections("", true)
	if err != nil {
		log.Printf("[BG_TOKEN_REFRESH] load active connections failed: %v", err)
		return
	}

	due := SelectConnectionsNeedingRefresh(conns, time.Now())
	for i, c := range due {
		refreshBackgroundConnection(ctx, repo, c)
		if i < len(due)-1 {
			delay := backgroundNormalDelay
			if sensitiveProviders[c.Provider] {
				delay = backgroundSensitiveDelay
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

// refreshBackgroundConnection refreshes one due connection and persists the
// result back to the row.
func refreshBackgroundConnection(ctx context.Context, repo *db.Repo, c ConnectionNeedingRefresh) {
	result, err := Refresh(ctx, &Params{
		Client:               &http.Client{Timeout: 15 * time.Second},
		Provider:             c.Provider,
		RefreshToken:         c.RefreshToken,
		AccessToken:          c.AccessToken,
		ProviderSpecificData: c.ProviderSpecificData,
	})
	if err != nil || result == nil || result.AccessToken == "" {
		log.Printf("[BG_TOKEN_REFRESH] refresh failed conn=%s provider=%s: %v", c.ID, c.Provider, err)
		return
	}

	conn, err := repo.GetProviderConnectionByID(c.ID)
	if err != nil || conn == nil {
		log.Printf("[BG_TOKEN_REFRESH] read row failed conn=%s: %v", c.ID, err)
		return
	}
	var existing map[string]any
	if conn.Data != "" {
		_ = json.Unmarshal([]byte(conn.Data), &existing)
	}
	if existing == nil {
		existing = make(map[string]any)
	}
	if result.ExpiresIn <= 0 {
		result = &TokenResult{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
			ExpiresIn:    defaultBackgroundExpiresIn,
			Scope:        result.Scope,
			ProjectID:    result.ProjectID,
		}
	}
	for k, v := range BuildConnectionUpdate(result) {
		existing[k] = v
	}
	// Kiro reports the AWS profile ARN; it belongs in providerSpecificData
	// (next to clientId/clientSecret), not as a top-level projectId.
	if result.ProjectID != "" {
		existing["projectId"] = result.ProjectID
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		log.Printf("[BG_TOKEN_REFRESH] marshal failed conn=%s: %v", c.ID, err)
		return
	}
	name := ""
	if conn.Name != nil {
		name = *conn.Name
	}
	priority := 0
	if conn.Priority != nil {
		priority = *conn.Priority
	}
	if err := repo.UpdateProviderConnection(c.ID, name, priority, conn.IsActive == 1, string(merged)); err != nil {
		log.Printf("[BG_TOKEN_REFRESH] persist failed conn=%s: %v", c.ID, err)
		return
	}
	log.Printf("[BG_TOKEN_REFRESH] refreshed conn=%s provider=%s", c.ID, c.Provider)
}
