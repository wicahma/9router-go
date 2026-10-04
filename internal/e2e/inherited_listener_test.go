package e2e

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.uber.org/fx"

	"9router/proxy/internal/app"
	"9router/proxy/internal/config"
)

// TestStartAdoptsInheritedListeningSocket covers the half of a zero-downtime
// self-update that the updater package cannot: the real fx wiring must serve on
// the socket the previous process image handed over instead of binding its own.
// The exec hop that produces such a socket is covered by
// updater.TestExecHandsOverTheListeningSocket.
func TestStartAdoptsInheritedListeningSocket(t *testing.T) {
	t.Setenv("JWT_SECRET", "e2e-inherited-listener-secret")

	inherited, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open the socket to hand over: %v", err)
	}
	defer inherited.Close()
	inheritedAddr := inherited.Addr().String()

	// The configured port is a different, free one on purpose: if the server
	// answers on the inherited address, the socket really was adopted rather
	// than rebound, and the configured port staying shut proves no second
	// listener was opened.
	configuredPort := freePort(t)

	t.Setenv("NINE_ROUTER_LISTENER_FD", strconv.Itoa(listenerFD(t, inherited)))

	dataDir := t.TempDir()
	fxApp := fx.New(
		app.ConfigModule,
		fx.Replace(&config.Config{
			DatabasePath: filepath.Join(dataDir, "data.sqlite"),
			Port:         configuredPort,
		}),
		app.DatabaseModule,
		app.HandlersModule,
		app.ServerModule,
		fx.NopLogger,
	)
	ctx := context.Background()
	if err := fxApp.Start(ctx); err != nil {
		t.Fatalf("start with an inherited socket failed: %v", err)
	}
	defer fxApp.Stop(ctx)

	// OnStart serves from a goroutine, so give the first request a moment to
	// land rather than racing the scheduler.
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for {
		resp, err := client.Get("http://" + inheritedAddr + "/api/version")
		if err == nil {
			resp.Body.Close()
			break
		}
		lastErr = err
		if time.Now().After(deadline) {
			t.Fatalf("the inherited socket is not being served: %v", lastErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if os.Getenv("NINE_ROUTER_LISTENER_FD") != "" {
		t.Error("the handed-over descriptor was not consumed")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(configuredPort)), 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatalf("the server bound its configured port %d instead of using the inherited socket", configuredPort)
	}
}

// listenerFD returns the descriptor number a TCP listener is bound to.
func listenerFD(t *testing.T, l net.Listener) int {
	t.Helper()
	tl, ok := l.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener is %T, want *net.TCPListener", l)
	}
	raw, err := tl.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var fd int
	if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
		t.Fatalf("control: %v", err)
	}
	return fd
}

// freePort reserves and releases a port, returning one that is free at the time
// of the call.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}
