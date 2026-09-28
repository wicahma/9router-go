package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/ratelimit"
)

func writeSetting(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return rec
}

// The dashboard already writes arbitrary settings keys through
// HandleUpdateSettings, so modelRps needs no new endpoint. But the hot path
// reads a registry loaded once at startup, so a settings write that does not
// refresh it would leave the change invisible until a restart.
func TestSettingsWritePushesRpsIntoTheLiveRegistry(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelRps":{"openai/gpt-4o":20}}`)

	if got := ratelimit.Shared().Limits()["openai/gpt-4o"]; got != 20 {
		t.Fatalf("live limit for openai/gpt-4o = %d, want 20 without a restart", got)
	}
}

// Non-positive values mean "no limit" and must never reach the registry, or a
// stray 0 would silently throttle a model to nothing.
func TestSettingsWriteDropsNonPositiveRps(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelRps":{"a":0,"b":-3,"c":5}}`)

	limits := ratelimit.Shared().Limits()
	if _, ok := limits["a"]; ok {
		t.Fatal("a 0-value limit must be dropped, not stored")
	}
	if _, ok := limits["b"]; ok {
		t.Fatal("a negative limit must be dropped, not stored")
	}
	if limits["c"] != 5 {
		t.Fatalf("limits = %v, want c=5", limits)
	}
}

// Writing a settings payload that does not mention modelRps must leave the
// limits alone. A refresh keyed on "settings were written" rather than on
// "modelRps was present" would clear every limit whenever an unrelated setting
// was saved.
func TestUnrelatedSettingsWriteLeavesRpsAlone(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelRps":{"openai/gpt-4o":20}}`)
	writeSetting(t, router, `{"rtkEnabled":true}`)

	if got := ratelimit.Shared().Limits()["openai/gpt-4o"]; got != 20 {
		t.Fatalf("openai/gpt-4o = %d, want it untouched at 20 by an unrelated save", got)
	}
}

// An empty map is how the UI removes every limit, so it must reach the
// registry as a clear rather than being ignored as "no change".
func TestEmptyRpsMapClearsLimits(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	t.Cleanup(func() { ratelimit.Shared().Load(nil) })
	router := setupTestRouter(repo)

	writeSetting(t, router, `{"modelRps":{"openai/gpt-4o":20}}`)
	writeSetting(t, router, `{"modelRps":{}}`)

	if got := ratelimit.Shared().Limits(); len(got) != 0 {
		t.Fatalf("limits = %v, want all cleared", got)
	}
}
