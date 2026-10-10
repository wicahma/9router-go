package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"go.uber.org/fx"

	"9router/proxy/internal/config"
	"9router/proxy/internal/db"
	"9router/proxy/internal/pricing"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy/oauth"
	"9router/proxy/internal/ratelimit"
	"9router/proxy/internal/retention"
	"9router/proxy/internal/shutdown"
	"9router/proxy/internal/updater"
)

// ServerModule provides *http.Server and manages its lifecycle and background tasks.
var ServerModule = fx.Module("server",
	fx.Provide(
		ProvideServer,
	),
	fx.Invoke(
		func(*http.Server) {},
	),
)

// ServerParams defines inputs for constructing the HTTP server and lifecycle hooks.
type ServerParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Config    *config.Config
	Repo      *db.Repo
	Handler   http.Handler
	CLIParams CLIParams
}

// Server timeouts are fixed rather than configurable on purpose: the values
// below bound how long one client can hold one connection, so making them
// tunable would put a Slowloris knob in reach of anyone who edits .env.
const (
	// serverReadHeaderTimeout caps how long a client may take to finish
	// sending request headers. Same value as the OAuth callback listener in
	// internal/proxy/oauth/codex_proxy.go.
	serverReadHeaderTimeout = 10 * time.Second
	// serverIdleTimeout closes keep-alive connections with no request in
	// flight. It does not apply while a request is being served, so SSE and
	// WebSocket streams run to their natural end.
	serverIdleTimeout = 120 * time.Second
	// serverMaxHeaderBytes states the header limit the gateway intends.
	// It does not close a hole: net/http falls back to
	// http.DefaultMaxHeaderBytes (1 MiB) when this is zero, so the enforced
	// value is the same either way. Pinning it keeps the limit a decision
	// here rather than a stdlib default this repo never reviewed.
	serverMaxHeaderBytes = 1 << 20
)

// ProvideServer creates *http.Server and registers lifecycle hooks.
func ProvideServer(p ServerParams) *http.Server {
	var addr string
	if p.Config.Host != "" {
		addr = net.JoinHostPort(p.Config.Host, strconv.Itoa(p.Config.Port))
	} else {
		addr = fmt.Sprintf(":%d", p.Config.Port)
	}
	server := &http.Server{
		Addr:    addr,
		Handler: p.Handler,
		// WriteTimeout stays unset on purpose: internal/proxy/stall.go lets an
		// SSE stream idle for DefaultStallTimeout (6 minutes), and a write
		// deadline would sever those streams mid-response.
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}

	// Requests being handled right now, keyed by connection. RestartSelf's
	// drain hook waits on this before it swaps the process image, because the
	// exec discards this address space and would cut a streaming reply mid-body.
	var (
		inflightMu sync.Mutex
		inflight   = map[net.Conn]struct{}{}
	)
	server.ConnState = func(c net.Conn, state http.ConnState) {
		inflightMu.Lock()
		defer inflightMu.Unlock()
		if state == http.StateActive {
			inflight[c] = struct{}{}
		} else {
			delete(inflight, c)
		}
	}

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			autoUpdate := p.CLIParams.AutoUpdate
			if !autoUpdate && p.Repo != nil {
				if settings, sErr := p.Repo.GetSettings(); sErr == nil && settings != nil {
					autoUpdate = settings.AutoUpdate
				}
			}
			updater.StartBackgroundCheck(shutdown.Context(), autoUpdate)
			log.Printf("[config] auto-update enabled=%v", autoUpdate)

			// Per-model RPS ceilings. GetSettings is an uncached SQLite read,
			// so this is loaded once here and re-loaded on every settings
			// write; the request path only ever reads the registry.
			if p.Repo != nil {
				if settings, sErr := p.Repo.GetSettings(); sErr == nil && settings != nil {
					ratelimit.Shared().Load(settings.ModelRps)
				} else if sErr != nil {
					log.Printf("[config] model rps: %v", sErr)
				}
				if n := len(ratelimit.Shared().Limits()); n > 0 {
					log.Printf("[config] model rps limits active=%d", n)
				}
			}

			catalogPath := filepath.Join(filepath.Dir(p.Config.DatabasePath), "model-catalog.json")
			providers.StartBackgroundCatalogSync(shutdown.Context(), nil, catalogPath)
			// pricing cannot import providers (cycle), so the catalog is handed
			// over as a lookup function at startup.
			pricing.CatalogPriceLookup = func(model string) (float64, float64, bool) {
				p, ok := providers.GetCatalogPrice(model)
				return p.InputPer1M, p.OutputPer1M, ok
			}
			oauth.StartBackgroundRefresh(shutdown.Context(), p.Repo)
			retention.StartBackground(shutdown.Context(), p.Repo, p.Config.Retention)
			if p.Config.Retention != nil {
				log.Printf("[config] retention enabled=%v interval=%v", p.Config.Retention.Enabled, p.Config.Retention.Interval)
			}

			log.Printf("9router-go Proxy (%s) starting on port %d", updater.CurrentVersion, p.Config.Port)

			// The listener is ours, not the http.Server's, so a self-update can
			// hand it to the next process image instead of closing and
			// reopening the port.
			listener, err := updater.ServeListener(addr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", addr, err)
			}

			updater.SetDrainHook(func(ctx context.Context) {
				for {
					inflightMu.Lock()
					pending := len(inflight)
					inflightMu.Unlock()
					if pending == 0 {
						return
					}
					select {
					case <-ctx.Done():
						log.Printf("[config] restart: %d request(s) still in flight, replacing the process anyway", pending)
						return
					case <-time.After(50 * time.Millisecond):
					}
				}
			})

			go func() {
				if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
					log.Printf("Server failed: %v", err)
				}
			}()

			// An adopted socket keeps the port the previous version was serving
			// on, which is what the banner should name rather than the
			// configured address.
			serving := listener.Addr().String()
			fmt.Fprintf(os.Stdout, "\n  🚀 9router-go Proxy (%s) on %s\n\n", updater.CurrentVersion, serving)
			log.Printf("Server is ready to handle requests at %s", serving)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			fmt.Fprintln(os.Stdout, "\n  Shutting down...")

			// Signal in-flight SSE streams to end promptly: the stall reader closes each
			// upstream body, handlers emit a final [DONE], and Shutdown completes well
			// within its deadline instead of waiting out the full timeout.
			shutdown.Cancel()

			// Refuse NEW keep-alive reuse immediately: idle browser/dashboard
			// connections otherwise hold Shutdown for the full deadline even
			// when zero requests are in flight (the "shutdown takes forever"
			// complaint). In-flight requests still drain gracefully below.
			server.SetKeepAlivesEnabled(false)

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("Server shutdown did not complete in time: %v", err)
			} else {
				log.Println("Server stopped gracefully")
			}
			return nil
		},
	})

	return server
}
