package db

import (
	"crypto/rand"
	json "encoding/json/v2"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"9router/proxy/internal/handlerutil"
)

// ProxyPool represents a pool of proxy URLs for routing requests.
type ProxyPool struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IsActive    bool     `json:"isActive"`
	URLs        []string `json:"urls"`
	Strategy    string   `json:"strategy"` // "round-robin" or "random"
	Type        string   `json:"type"`     // "http", "vercel", "cloudflare", "deno"
	NoProxy     string   `json:"noProxy"`
	StrictProxy bool     `json:"strictProxy"`
	index       uint64   // atomic counter for round-robin

	// mu guards health. health is the live-traffic verdict per proxy URL
	// (in-memory only, never persisted — proxyPools.data stays
	// byte-compatible). proxyPoolCache shares *ProxyPool across goroutines
	// and NextURL is lock-free atomic, so map access must stay guarded;
	// p.URLs itself is never mutated by the health path.
	mu     sync.RWMutex
	health map[string]*urlHealth
}

// urlHealth is the live-traffic verdict for one proxy URL.
type urlHealth struct {
	consecutiveFails uint32
	lastFailAt       time.Time // UTC
	lastOKAt         time.Time // UTC
}

const (
	// urlCooldownFails parks a URL after this many consecutive live-traffic
	// failures. A single blip (1-2 fails) never sidelines a URL.
	urlCooldownFails = 3
	// urlCooldownWindow is how long a parked URL is skipped before it is
	// retried (self-healing when the free proxy comes back). An OK report
	// clears the park immediately.
	urlCooldownWindow = 5 * time.Minute
)

var proxyPoolCache sync.Map // map[string]*ProxyPool

// GetProxyPool reads a proxy pool from the proxyPools table.
func (r *Repo) GetProxyPool(poolID string) (*ProxyPool, error) {
	var data string
	var isActive int
	err := r.db.QueryRow(
		`SELECT data, isActive FROM proxyPools WHERE id = ?`, poolID,
	).Scan(&data, &isActive)
	if err != nil {
		return nil, fmt.Errorf("proxy pool %s: %w", poolID, err)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("parse pool data: %w", err)
	}

	pool := &ProxyPool{
		ID:       poolID,
		IsActive: isActive == 1,
		Name:     handlerutil.GetString(raw, "name"),
		Strategy: handlerutil.GetString(raw, "strategy"),
	}

	if urls, ok := raw["urls"].([]any); ok {
		for _, u := range urls {
			if s, ok := u.(string); ok && s != "" {
				pool.URLs = append(pool.URLs, s)
			}
		}
	}
	if len(pool.URLs) == 0 {
		if singleURL := handlerutil.GetString(raw, "proxyUrl"); singleURL != "" {
			pool.URLs = append(pool.URLs, singleURL)
		}
	}

	pool.Type = handlerutil.GetString(raw, "type")
	if pool.Type == "" {
		pool.Type = "http"
	}
	pool.NoProxy = handlerutil.GetString(raw, "noProxy")
	if sp, ok := raw["strictProxy"].(bool); ok {
		pool.StrictProxy = sp
	}

	if pool.Strategy == "" {
		pool.Strategy = "round-robin"
	}

	if cached, ok := proxyPoolCache.Load(poolID); ok {
		if pool, ok := cached.(*ProxyPool); ok {
			return pool, nil
		}
	}
	// Store the freshly-read pool. If another goroutine won the race, return
	// its value instead of overwriting — never mutate a value in the cache,
	// since concurrent readers use it lock-free via NextURL.
	actual, _ := proxyPoolCache.LoadOrStore(poolID, pool)
	if pool, ok := actual.(*ProxyPool); ok {
		return pool, nil
	}
	return pool, nil
}

// NextURL returns the next proxy URL using round-robin selection, skipping
// URLs currently cooling down (3 consecutive live-traffic fails within 5m).
// It iterates at most len(URLs) positions from the atomic counter and returns
// the first live one; when every URL is cooling down it returns the counter's
// pick (same as the old blind round-robin) — a maybe-dead proxy beats no
// proxy under non-strict mode, and the caller decides the fallback.
func (p *ProxyPool) NextURL() string {
	if len(p.URLs) == 0 {
		return ""
	}
	idx := atomic.AddUint64(&p.index, 1)
	n := uint64(len(p.URLs))
	for i := uint64(0); i < n; i++ {
		u := p.URLs[(idx+i)%n]
		if !p.isURLCoolingDown(u) {
			return u
		}
	}
	return p.URLs[idx%n]
}

// ReportURLResult records a live-traffic verdict for one proxy URL.
// ok=true resets the fail streak and stamps lastOKAt; ok=false bumps the
// streak and stamps lastFailAt. Lazily creates the health entry.
func (p *ProxyPool) ReportURLResult(url string, ok bool) {
	if url == "" {
		return
	}
	now := time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.health == nil {
		p.health = make(map[string]*urlHealth)
	}
	h, found := p.health[url]
	if !found {
		h = &urlHealth{}
		p.health[url] = h
	}
	if ok {
		h.consecutiveFails = 0
		h.lastOKAt = now
		return
	}
	h.consecutiveFails++
	h.lastFailAt = now
}

// isURLCoolingDown reports whether url is parked: 3+ consecutive fails with
// the latest fail inside the 5-minute window.
func (p *ProxyPool) isURLCoolingDown(url string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.health == nil {
		return false
	}
	h, found := p.health[url]
	if !found {
		return false
	}
	if h.consecutiveFails < urlCooldownFails {
		return false
	}
	return time.Since(h.lastFailAt) < urlCooldownWindow
}

// AllCoolingDown reports whether every URL in the pool is currently parked.
// LiveURLs deliberately returns the full list in that case (never empty), so
// callers that need to tell "has live URLs" from "all parked" — e.g. the
// chat failover path deciding between rotation and a fallback pool — use this.
func (p *ProxyPool) AllCoolingDown() bool {
	if len(p.URLs) == 0 {
		return false
	}
	for _, u := range p.URLs {
		if !p.isURLCoolingDown(u) {
			return false
		}
	}
	return true
}

// LiveURLs returns the pool's URLs not currently cooling down, in pool order.
// Empty p.URLs yields nil. When every URL is cooling down the full list is
// returned anyway — never empty while URLs exist — so the caller (not this
// func) decides between a maybe-dead proxy, a fallback pool, or direct.
func (p *ProxyPool) LiveURLs() []string {
	if len(p.URLs) == 0 {
		return nil
	}
	live := make([]string, 0, len(p.URLs))
	for _, u := range p.URLs {
		if !p.isURLCoolingDown(u) {
			live = append(live, u)
		}
	}
	if len(live) == 0 {
		out := make([]string, len(p.URLs))
		copy(out, p.URLs)
		return out
	}
	return live
}

// ProxyPoolData is the JSON payload stored in the proxyPools.data column for
// deploy-type pools. Field order matches what the Next.js dashboard writes so
// the shared DB stays byte-compatible.
type ProxyPoolData struct {
	Name         string  `json:"name"`
	ProxyURL     string  `json:"proxyUrl"`
	NoProxy      string  `json:"noProxy"`
	Type         string  `json:"type"`
	StrictProxy  bool    `json:"strictProxy"`
	LastTestedAt *string `json:"lastTestedAt"`
	LastError    *string `json:"lastError"`
}

// InsertProxyPool inserts a new proxy pool row and returns the pool object in
// the same shape as the dashboard's createProxyPool result.
func (r *Repo) InsertProxyPool(d ProxyPoolData) (map[string]any, error) {
	id := randomID()
	now := time.Now().UTC().Format(time.RFC3339)
	dataBytes, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal pool data: %w", err)
	}
	_, err = r.db.Exec(
		`INSERT INTO proxyPools (id, isActive, testStatus, data, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?)`,
		id, 1, "unknown", string(dataBytes), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert proxy pool: %w", err)
	}
	return map[string]any{
		"id":           id,
		"name":         d.Name,
		"proxyUrl":     d.ProxyURL,
		"noProxy":      d.NoProxy,
		"type":         d.Type,
		"strictProxy":  d.StrictProxy,
		"isActive":     true,
		"testStatus":   "unknown",
		"lastTestedAt": nil,
		"lastError":    nil,
		"createdAt":    now,
		"updatedAt":    now,
	}, nil
}

// randomID returns a random hex id (uuid-shaped, like the dashboard's uuidv4).
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pool-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// ListProxyPools returns all proxy pools with their data unpacked.
func (r *Repo) ListProxyPools() ([]map[string]any, error) {
	rows, err := r.db.Query(`SELECT id, isActive, testStatus, data, createdAt, updatedAt FROM proxyPools ORDER BY createdAt DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []map[string]any
	for rows.Next() {
		var id, testStatus, dataStr, createdAt, updatedAt string
		var isActiveInt int
		if err := rows.Scan(&id, &isActiveInt, &testStatus, &dataStr, &createdAt, &updatedAt); err != nil {
			continue
		}
		var poolData map[string]any
		if err := json.Unmarshal([]byte(dataStr), &poolData); err != nil {
			poolData = make(map[string]any)
		}
		poolData["id"] = id
		poolData["isActive"] = (isActiveInt == 1)
		poolData["testStatus"] = testStatus
		poolData["createdAt"] = createdAt
		poolData["updatedAt"] = updatedAt
		list = append(list, poolData)
	}
	if list == nil {
		list = []map[string]any{}
	}
	return list, nil
}

// DeleteProxyPool removes a proxy pool by id.
func (r *Repo) DeleteProxyPool(id string) error {
	_, err := r.db.Exec(`DELETE FROM proxyPools WHERE id = ?`, id)
	proxyPoolCache.Delete(id)
	return err
}

// UpdateProxyPool updates a proxy pool's data, isActive, and testStatus.
func (r *Repo) UpdateProxyPool(id string, updates map[string]any) error {
	var dataStr string
	var isActive int
	var testStatus string
	err := r.db.QueryRow(`SELECT data, isActive, testStatus FROM proxyPools WHERE id = ?`, id).Scan(&dataStr, &isActive, &testStatus)
	if err != nil {
		return err
	}
	var existing map[string]any
	_ = json.Unmarshal([]byte(dataStr), &existing)
	if existing == nil {
		existing = make(map[string]any)
	}
	for k, v := range updates {
		if k == "isActive" {
			if b, ok := v.(bool); ok {
				if b {
					isActive = 1
				} else {
					isActive = 0
				}
			}
		} else if k == "testStatus" {
			if s, ok := v.(string); ok {
				testStatus = s
			}
		} else if k != "id" && k != "createdAt" && k != "updatedAt" {
			existing[k] = v
		}
	}
	updatedBytes, _ := json.Marshal(existing)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = r.db.Exec(`UPDATE proxyPools SET data = ?, isActive = ?, testStatus = ?, updatedAt = ? WHERE id = ?`,
		string(updatedBytes), isActive, testStatus, now, id)
	proxyPoolCache.Delete(id)
	return err
}

// SetProxyPoolStatus updates only testStatus and lastTestedAt on a pool.
func (r *Repo) SetProxyPoolStatus(id string, status string, latencyMs int64) error {
	var dataStr string
	var isActive int
	var currentStatus string
	err := r.db.QueryRow(`SELECT data, isActive, testStatus FROM proxyPools WHERE id = ?`, id).Scan(&dataStr, &isActive, &currentStatus)
	if err != nil {
		return err
	}
	var existing map[string]any
	_ = json.Unmarshal([]byte(dataStr), &existing)
	if existing == nil {
		existing = make(map[string]any)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	existing["lastTestedAt"] = now
	existing["latency"] = latencyMs
	updatedBytes, _ := json.Marshal(existing)
	_, err = r.db.Exec(`UPDATE proxyPools SET data = ?, testStatus = ?, updatedAt = ? WHERE id = ?`,
		string(updatedBytes), status, now, id)
	proxyPoolCache.Delete(id)
	return err
}
