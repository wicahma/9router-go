// Package e2e drives the server through its real fx wiring — config, database
// bootstrap, router — against an empty data directory, so a first run is
// covered end to end rather than by a handler test with a hand-built schema.
package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/fx"

	"9router/proxy/internal/app"
	"9router/proxy/internal/auth"
	"9router/proxy/internal/config"
	"9router/proxy/internal/db"
)

// TestFreshInstallPasswordRotation is the first-run gate: no database file, the
// production wiring, and the login / set-password / session sequence the owner
// goes through from a tunnel or LAN origin. It regresses the failure where the
// rotation wrote settings on a database that had no settings table yet
// ("update settings raw: SQL logic error: no such table: settings (1)").
func TestFreshInstallPasswordRotation(t *testing.T) {
	t.Setenv("JWT_SECRET", "e2e-fresh-install-secret")
	auth.ResetLoginLimiter()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "data.sqlite")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected no database before the first start, got err=%v", err)
	}

	var (
		handler http.Handler
		repo    *db.Repo
	)
	fxApp := fx.New(
		app.ConfigModule,
		fx.Replace(&config.Config{DatabasePath: dbPath, Port: 20199}),
		app.DatabaseModule,
		app.HandlersModule,
		fx.NopLogger,
		fx.Populate(&handler, &repo),
	)
	ctx := context.Background()
	if err := fxApp.Start(ctx); err != nil {
		t.Fatalf("first start on an empty data dir failed: %v", err)
	}
	defer fxApp.Stop(ctx)

	// The bootstrap has to hand the dashboard a usable schema, not just a file.
	for _, table := range []string{"settings", "apiKeys", "providerConnections", "kv"} {
		var n int
		if err := repo.RawDB().QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&n); err != nil {
			t.Fatalf("inspect sqlite_master for %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("expected table %s to exist after the first start", table)
		}
	}

	remote := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1234"
		req.Host = "router.example.com"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// The compatibility default authenticates but withholds a session until the
	// password is rotated.
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"123456"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the remote default password, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := remote(http.MethodPost, "/api/auth/set-password", `{"currentPassword":"123456","newPassword":"e2e-pass-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from set-password on a fresh database, got %d: %s", rec.Code, rec.Body.String())
	}

	// The rotation is durable: the hash is in the seeded settings row.
	var stored string
	if err := repo.RawDB().QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read the settings row after rotation: %v", err)
	}
	if !strings.Contains(stored, `"password":"$2`) {
		t.Errorf("expected a bcrypt hash in the settings row, got %s", stored)
	}

	rec = remote(http.MethodPost, "/api/auth/login", `{"password":"e2e-pass-1"}`)
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
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected GET /api/settings to return 200 with the session cookie, got %d: %s", w.Code, w.Body.String())
	}

	// The default is gone and the endpoint refuses to act as a session-less
	// password change now that a hash is stored.
	auth.ResetLoginLimiter()
	if rec := remote(http.MethodPost, "/api/auth/login", `{"password":"123456"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected the default password to stop working, got %d: %s", rec.Code, rec.Body.String())
	}
	auth.ResetLoginLimiter()
	if rec := remote(http.MethodPost, "/api/auth/set-password", `{"currentPassword":"e2e-pass-1","newPassword":"e2e-pass-2"}`); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 once a password is stored, got %d: %s", rec.Code, rec.Body.String())
	}
}
