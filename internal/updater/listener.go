package updater

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"9router/proxy/internal/log"
)

// inheritedListenerEnv names the file descriptor a previous image of this
// process leaves open for its replacement: the listening socket, deliberately
// not CLOEXEC, so it survives the exec. That is the whole trick behind a
// zero-downtime self-update when the gateway itself is PID 1 of its container —
// the port is never closed, so there is no window where connections are
// refused and no "address already in use" race between the old and new
// process.
const inheritedListenerEnv = "NINE_ROUTER_LISTENER_FD"

// restartDrainTimeout bounds how long a restart waits for in-flight requests
// before replacing the process image. A reply that is still streaming gets to
// finish; a wedged one does not hold an update hostage.
const restartDrainTimeout = 30 * time.Second

var (
	// handoffFDNum is the socket this process listens on, recorded by
	// ServeListener and handed to the next image by RestartSelf. -1 means "no
	// socket to pass on" (Windows, or the server never started).
	handoffMu    sync.Mutex
	handoffFDNum = -1
	drainFn      func(context.Context)
)

// SetDrainHook registers the function RestartSelf runs before it replaces the
// process image. app wires the HTTP server's in-flight drain here; without a
// hook a restart swaps immediately.
func SetDrainHook(fn func(context.Context)) {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	drainFn = fn
}

// ServeListener returns the HTTP listener for addr. When the environment
// carries a descriptor from a previous image it adopts that socket instead of
// binding a new one — the port was never released, so there is nothing to
// rebind. The listener this process opens itself is recorded so a later
// RestartSelf can pass it on.
func ServeListener(addr string) (net.Listener, error) {
	if fd, ok := inheritedListenerFD(); ok {
		if l, err := listenerFromFD(fd); err == nil {
			// Consumed: a later hand-off re-derives its own descriptor.
			os.Unsetenv(inheritedListenerEnv)
			log.Info("updater", "took over the listening socket from the previous version", "fd", fd)
			return l, nil
		} else {
			// Its owner is gone but the socket is unusable, so fall back to a
			// fresh bind rather than refusing to start at all.
			log.Warn("updater", "inherited listening socket unusable, binding a new one", "fd", fd, "error", err)
		}
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if tl, ok := l.(*net.TCPListener); ok {
		if raw, err := tl.SyscallConn(); err == nil {
			_ = raw.Control(func(fd uintptr) { recordHandoffFD(int(fd)) })
		}
	}
	return l, nil
}

// inheritedListenerFD reads the descriptor an earlier image left behind.
func inheritedListenerFD() (int, bool) {
	raw := os.Getenv(inheritedListenerEnv)
	if raw == "" {
		return 0, false
	}
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 0 {
		log.Warn("updater", "ignoring unparseable inherited listener descriptor", "value", raw)
		return 0, false
	}
	return fd, true
}

func currentHandoffFD() int {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	return handoffFDNum
}

// registeredDrain returns the registered drain hook, if any.
func registeredDrain() func(context.Context) {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	return drainFn
}
