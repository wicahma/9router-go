package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerCacheControl(t *testing.T) {
	h := Handler()

	asset := "assets/index-UTtW_rdi.js"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+asset, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("asset %s: status = %d, want 200", asset, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset %s: Cache-Control = %q, want immutable", asset, got)
	}

	for _, path := range []string{"/", "/dashboard/combos"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, "no-store")
		}
		if !strings.Contains(rec.Body.String(), "text/html") && rec.Header().Get("Content-Type") == "" {
			t.Errorf("%s: empty response body and no Content-Type", path)
		}
	}
}
