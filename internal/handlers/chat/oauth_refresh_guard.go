package chat

import "sync"

// refreshInFlight tracks connections with an OAuth refresh currently running.
// Concurrent refreshes for the same connection collapse into one so parallel
// 401s don't fire N simultaneous refresh POSTs at Google.
var (
	refreshMu       sync.Mutex
	refreshInFlight = make(map[string]struct{})
)

// acquireRefresh returns true if the caller owns the refresh for connID.
// Returns false if another goroutine is already refreshing it, or if connID
// is empty (callers must never refresh with an empty id).
func acquireRefresh(connID string) bool {
	if connID == "" {
		return false
	}
	refreshMu.Lock()
	defer refreshMu.Unlock()
	if _, ok := refreshInFlight[connID]; ok {
		return false
	}
	refreshInFlight[connID] = struct{}{}
	return true
}

// releaseRefresh releases ownership of the refresh for connID. Safe to call
// for an id never acquired (no-op). Callers must defer this immediately after
// a successful acquire so a panic or early return never wedges the key.
func releaseRefresh(connID string) {
	if connID == "" {
		return
	}
	refreshMu.Lock()
	defer refreshMu.Unlock()
	delete(refreshInFlight, connID)
}
