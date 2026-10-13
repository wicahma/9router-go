package chat

import (
	"9router/proxy/internal/constants"
	"9router/proxy/internal/log"
	"net/http"
	"net/url"
	"sync"
)

// maxProxyClients caps rotating-proxy growth: each entry pins a Transport
// plus idle sockets, so unbounded distinct URLs (residential rotation)
// would leak descriptors without eviction.
const maxProxyClients = 128

var (
	proxyClientsMu sync.RWMutex
	proxyClients   = make(map[string]*http.Client)
	// proxyPoolLogged tracks pool IDs already announced at debug level so the
	// "routing via proxy" line appears once per pool, not on every request.
	proxyPoolLogged sync.Map
)

// logProxyOnce records proxy usage once per pool at debug level, e.g.
// proxy routing requests via proxy pool pool=abc123 url=http://... type=http
// Run with LOG_LEVEL=debug and grep for "proxy" to confirm traffic uses the pool.
func logProxyOnce(poolID, proxyURLStr, proxyType string) {
	if poolID == "" {
		return
	}
	if _, loaded := proxyPoolLogged.LoadOrStore(poolID, true); loaded {
		return
	}
	if proxyType == "" {
		proxyType = "http"
	}
	log.Debug("proxy", "routing requests via proxy pool", "pool", poolID, "url", proxyURLStr, "type", proxyType)
}

// GetClientForConnection returns an http.Client configured with ProxyPool transport if set.
func (h *ChatHandler) GetClientForConnection(connData *ConnectionData) *http.Client {
	return h.getClientForConnection(connData)
}

// resolveProxyURLs returns the ordered candidate proxy URLs for connData:
// bound pool's LiveURLs() first, then (if pool bound but fully cooling down
// or empty) the best-live fallback pool's URLs. Nil when direct.
// Second return is strictProxy for the resolved scope.
func (h *ChatHandler) resolveProxyURLs(connData *ConnectionData) ([]string, bool) {
	if connData == nil || h.Repo == nil || connData.ProxyPoolID == "" {
		return nil, false
	}
	pool, err := h.Repo.GetProxyPool(connData.ProxyPoolID)
	if err != nil || pool == nil || !pool.IsActive {
		return nil, false
	}
	if pool.Type == "vercel" || pool.Type == "cloudflare" || pool.Type == "deno" {
		return nil, false
	}
	strict := pool.StrictProxy
	live := pool.LiveURLs()
	if len(live) > 0 && !pool.AllCoolingDown() {
		// Bound pool still has live URLs. NextURL() advances the atomic
		// counter (LiveURLs does not), so call it first to preserve
		// rotation; if its pick is cooling down, LiveURLs order wins.
		pick := pool.NextURL()
		for _, u := range live {
			if u == pick {
				out := make([]string, 0, len(live))
				out = append(out, pick)
				for _, v := range live {
					if v != pick {
						out = append(out, v)
					}
				}
				return out, strict
			}
		}
		return live, strict
	}
	// Bound pool empty or fully cooling down: REPLACE the dead list with the
	// best-live fallback pool's URLs (never append — dead URLs are known-bad
	// right now and would just re-burn timeouts).
	fbURLs, fbStrict, fbID := h.listBestLiveFallback(connData.ProxyPoolID)
	if len(fbURLs) == 0 {
		return nil, strict
	}
	log.Info("proxy", "proxy failover", "pool", connData.ProxyPoolID, "fallback", fbID)
	return fbURLs, strict || fbStrict
}

// listBestLiveFallback returns the active, non-bound pool with the lowest
// recorded latency whose testStatus == "passed": its live URLs, strict flag,
// and id. Pure DB read, no network I/O, safe on the hot path.
func (h *ChatHandler) listBestLiveFallback(boundID string) ([]string, bool, string) {
	if h.Repo == nil {
		return nil, false, ""
	}
	pools, err := h.Repo.ListProxyPools()
	if err != nil || len(pools) == 0 {
		return nil, false, ""
	}
	bestID := ""
	var bestLatency float64
	first := true
	bestStrict := false
	for _, pm := range pools {
		id, _ := pm["id"].(string)
		if id == "" || id == boundID {
			continue
		}
		if active, _ := pm["isActive"].(bool); !active {
			continue
		}
		if st, _ := pm["testStatus"].(string); st != "passed" {
			continue
		}
		if typ, _ := pm["type"].(string); typ == "vercel" || typ == "cloudflare" || typ == "deno" {
			continue
		}
		var lat float64
		switch v := pm["latency"].(type) {
		case float64:
			lat = v
		case float32:
			lat = float64(v)
		case int:
			lat = float64(v)
		case int64:
			lat = float64(v)
		}
		if first || lat < bestLatency {
			first = false
			bestLatency = lat
			bestID = id
			bestStrict, _ = pm["strictProxy"].(bool)
		}
	}
	if bestID == "" {
		return nil, false, ""
	}
	fb, err := h.Repo.GetProxyPool(bestID)
	if err != nil || fb == nil || !fb.IsActive || len(fb.URLs) == 0 {
		return nil, false, ""
	}
	return fb.LiveURLs(), bestStrict || fb.StrictProxy, bestID
}

// NoteProxyResult records a live-traffic verdict for connData's proxy URL.
// ok=false only for transport-level failures (proxy dial/connect timeout),
// NEVER for upstream HTTP statuses (401/429/500 mean the proxy WORKED).
func (h *ChatHandler) NoteProxyResult(connData *ConnectionData, proxyURL string, ok bool) {
	if connData == nil || h.Repo == nil || proxyURL == "" || connData.ProxyPoolID == "" {
		return
	}
	pool, err := h.Repo.GetProxyPool(connData.ProxyPoolID)
	if err != nil || pool == nil {
		return
	}
	if pool.Type == "vercel" || pool.Type == "cloudflare" || pool.Type == "deno" {
		return
	}
	pool.ReportURLResult(proxyURL, ok)
}

// clientForConnectionWithURL is getClientForConnection plus the chosen proxy
// URL. getClientForConnection delegates to it, discarding the URL. fallback.go
// calls the WithURL variant so NoteProxyResult can report the exact URL used.
func (h *ChatHandler) clientForConnectionWithURL(connData *ConnectionData) (*http.Client, string) {
	client := h.getClientForConnection(connData)
	if connData == nil || h.Repo == nil || connData.ProxyPoolID == "" {
		return client, ""
	}
	urls, _ := h.resolveProxyURLs(connData)
	if len(urls) == 0 {
		return client, ""
	}
	return client, urls[0]
}

func (h *ChatHandler) getClientForConnection(connData *ConnectionData) *http.Client {
	if connData == nil {
		return h.Client
	}

	var proxyURLStr string
	var proxyType string
	var strictProxy bool

	// 1. Resolve from ProxyPool (live URLs first, best-live fallback replace)
	if connData.ProxyPoolID != "" && h.Repo != nil {
		if urls, strict := h.resolveProxyURLs(connData); len(urls) > 0 {
			proxyURLStr = urls[0]
			strictProxy = strict
			if pool, err := h.Repo.GetProxyPool(connData.ProxyPoolID); err == nil && pool != nil {
				proxyType = pool.Type
			}
		} else if pool, err := h.Repo.GetProxyPool(connData.ProxyPoolID); err == nil && pool != nil && pool.IsActive {
			proxyURLStr = pool.NextURL()
			proxyType = pool.Type
			strictProxy = pool.StrictProxy
		}
	}

	// 2. Fallback to legacy connection proxy
	if proxyURLStr == "" {
		proxyEnabled := connData.ConnectionProxyEnabled
		proxyURL := connData.ConnectionProxyURL
		if !proxyEnabled && connData.ProviderSpecificData != nil {
			if en, ok := connData.ProviderSpecificData["connectionProxyEnabled"].(bool); ok {
				proxyEnabled = en
			}
			if u, ok := connData.ProviderSpecificData["connectionProxyUrl"].(string); ok {
				proxyURL = u
			}
			if sp, ok := connData.ProviderSpecificData["strictProxy"].(bool); ok {
				strictProxy = sp
			}
		}
		if proxyEnabled && proxyURL != "" {
			proxyURLStr = proxyURL
			proxyType = "http"
		}
	}

	if proxyURLStr == "" {
		return h.Client
	}
	logProxyOnce(connData.ProxyPoolID, proxyURLStr, proxyType)

	parsedURL, err := url.Parse(proxyURLStr)
	if err != nil {
		log.Warn("proxy", "invalid proxy pool url", "pool", connData.ProxyPoolID, "url", proxyURLStr, "error", err)
		if strictProxy {
			log.Error("proxy", "strict proxy enabled but proxy url invalid", "url", proxyURLStr)
		}
		return h.Client
	}

	if proxyType == "http" || proxyType == "" {
		proxyClientsMu.RLock()
		client, ok := proxyClients[proxyURLStr]
		proxyClientsMu.RUnlock()
		if ok {
			return client
		}

		proxyClientsMu.Lock()
		defer proxyClientsMu.Unlock()
		if client, ok = proxyClients[proxyURLStr]; ok {
			return client
		}
		// Evict idle sockets of a random victim when over cap (amortized O(1);
		// exact LRU is overkill — URLs are hot or dead, never warm).
		if len(proxyClients) >= maxProxyClients {
			for victimURL, victim := range proxyClients {
				victim.CloseIdleConnections()
				delete(proxyClients, victimURL)
				break
			}
		}

		var baseTransport *http.Transport
		if origT, ok := http.DefaultTransport.(*http.Transport); ok {
			baseTransport = origT.Clone()
		} else {
			baseTransport = constants.DefaultHTTPTransportConfig.NewTransport()
		}
		constants.DefaultHTTPTransportConfig.Configure(baseTransport)
		baseTransport.Proxy = http.ProxyURL(parsedURL)
		client = &http.Client{
			Transport: baseTransport,
			Timeout:   h.Client.Timeout,
		}
		proxyClients[proxyURLStr] = client
		return client
	}

	// For Edge Relays (vercel, cloudflare, deno), standard client is used because
	// URL rewriting and x-relay headers are handled at request time.
	return h.Client
}
