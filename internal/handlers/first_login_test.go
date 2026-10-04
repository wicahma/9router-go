package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

// TestFirstLoginRemoteRotation covers the fresh-install login flow over a
// non-loopback origin — a tunnel hostname or a LAN address — where
// HandleAuthLogin accepts the compatibility default password but refuses to
// issue a session until the password is rotated.
func TestFirstLoginRemoteRotation(t *testing.T) {
	t.Setenv("JWT_SECRET", "first-login-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	auth.ResetLoginLimiter()

	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	remote := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1234"
		req.Host = "router.example.com"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// The default password authenticates but yields no session.
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"123456"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the remote default password, got %d: %s", rec.Code, rec.Body.String())
	}

	// The rotating call must not need the session cookie that login withheld.
	rec := remote(http.MethodPost, "/api/auth/set-password", `{"currentPassword":"123456","newPassword":"s3cret-pass"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from set-password, got %d: %s", rec.Code, rec.Body.String())
	}

	// The new password logs in and the dashboard API answers with its cookie.
	rec = remote(http.MethodPost, "/api/auth/login", `{"password":"s3cret-pass"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 logging in with the rotated password, got %d: %s", rec.Code, rec.Body.String())
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("expected a session cookie after rotating the default password")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(session)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected GET /api/settings to return 200 with the session cookie, got %d", w.Code)
	}

	// The compatibility default is gone.
	auth.ResetLoginLimiter()
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"123456"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected the default password to stop working, got %d: %s", rec.Code, rec.Body.String())
	}
	auth.ResetLoginLimiter()

	// Once a hash is stored the endpoint refuses, so it cannot be used as a
	// session-less password change later on.
	if rec := remote(http.MethodPost, "/api/auth/set-password", `{"currentPassword":"s3cret-pass","newPassword":"another-pass"}`); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 once a password is stored, got %d: %s", rec.Code, rec.Body.String())
	}
	auth.ResetLoginLimiter()
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"another-pass"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("rejected set-password must not have changed the password, got %d", rec.Code)
	}
	auth.ResetLoginLimiter()
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"s3cret-pass"}`); rec.Code != http.StatusOK {
		t.Errorf("expected the rotated password to keep working, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthSetPasswordRejectsWrongCurrentPassword checks that the endpoint is
// not a password oracle: it verifies the current password before rotating, and
// shares the login lockout so it cannot be brute-forced.
func TestAuthSetPasswordRejectsWrongCurrentPassword(t *testing.T) {
	t.Setenv("JWT_SECRET", "first-login-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	auth.ResetLoginLimiter()

	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/set-password",
		strings.NewReader(`{"currentPassword":"not-the-password","newPassword":"s3cret-pass"}`))
	req.RemoteAddr = "203.0.113.11:1234"
	req.Host = "router.example.com"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong current password, got %d: %s", rec.Code, rec.Body.String())
	}

	// Five failures lock the client out, exactly like login.
	for range 4 {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/set-password",
			strings.NewReader(`{"currentPassword":"not-the-password","newPassword":"s3cret-pass"}`))
		req.RemoteAddr = "203.0.113.11:1234"
		req.Host = "router.example.com"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/auth/set-password",
		strings.NewReader(`{"currentPassword":"123456","newPassword":"s3cret-pass"}`))
	req.RemoteAddr = "203.0.113.11:1234"
	req.Host = "router.example.com"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after repeated failures, got %d: %s", rec.Code, rec.Body.String())
	}
}
