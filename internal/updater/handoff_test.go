package updater

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The hand-off is the part of a self-update that can only fail at the worst
// moment, so it is tested against real sockets and — for the exec hop — a real
// process replacement of this test binary.

const (
	handoffTestEnv  = "NINE_ROUTER_HANDOFF_TEST" // parent | child
	handoffAddrEnv  = "NINE_ROUTER_HANDOFF_ADDR" // port the child must end up on
	handoffDrainEnv = "NINE_ROUTER_HANDOFF_DRAINED"
)

func resetHandoff(t *testing.T) {
	t.Helper()
	t.Setenv(inheritedListenerEnv, "")
	recordHandoffFD(-1)
	SetDrainHook(nil)
}

func TestServeListenerRecordsItsSocket(t *testing.T) {
	resetHandoff(t)

	l, err := ServeListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ServeListener: %v", err)
	}
	defer l.Close()

	if currentHandoffFD() < 0 {
		t.Fatal("ServeListener did not record its socket, so a restart could not hand the port over")
	}
}

// The whole point: an inherited descriptor is adopted instead of rebound, so the
// port is served by the new image on the socket the old one never released.
func TestServeListenerAdoptsInheritedSocket(t *testing.T) {
	resetHandoff(t)

	original, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer original.Close()

	raw, err := original.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var fd int
	if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
		t.Fatalf("control: %v", err)
	}
	t.Setenv(inheritedListenerEnv, fmt.Sprint(fd))

	// Deliberately a port the test cannot rely on: if the descriptor were
	// ignored, this would either fail or land somewhere else than the inherited
	// socket's address.
	adopted, err := ServeListener("127.0.0.1:1")
	if err != nil {
		t.Fatalf("ServeListener: %v", err)
	}
	defer adopted.Close()

	if got, want := adopted.Addr().String(), original.Addr().String(); got != want {
		t.Fatalf("adopted listener on %s, want the inherited socket %s", got, want)
	}
	if os.Getenv(inheritedListenerEnv) != "" {
		t.Fatal("the descriptor was not consumed, so the next restart would re-adopt a stale one")
	}
}

func TestServeListenerStillStartsWhenTheInheritedSocketIsUnusable(t *testing.T) {
	resetHandoff(t)
	t.Setenv(inheritedListenerEnv, "9999") // almost certainly not an open descriptor

	l, err := ServeListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("an unusable inherited descriptor must not stop the server from starting: %v", err)
	}
	defer l.Close()

	if currentHandoffFD() < 0 {
		t.Fatal("the fallback listener was not recorded for the next hand-off")
	}
}

func TestServeListenerIgnoresAGarbledDescriptor(t *testing.T) {
	resetHandoff(t)
	t.Setenv(inheritedListenerEnv, "not-a-number")

	l, err := ServeListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ServeListener: %v", err)
	}
	defer l.Close()
}

// Go opens every socket close-on-exec, which is what would snap the port shut
// during the restart; this is the one syscall that prevents it.
func TestClearCloseOnExec(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fd := f.Fd()
	if before, _, _ := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_GETFD), 0); before&1 == 0 {
		t.Fatalf("test setup: descriptor %d is not close-on-exec to begin with", fd)
	}

	if err := clearCloseOnExec(fd); err != nil {
		t.Fatalf("clearCloseOnExec: %v", err)
	}

	if after, _, _ := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_GETFD), 0); after&1 != 0 {
		t.Fatal("FD_CLOEXEC is still set, so the socket would be closed by the exec")
	}
}

// TestExecHandsOverTheListeningSocket drives the real thing: a subprocess
// listens, execs this same test binary in place of itself, and the replacement
// asserts it came up on the inherited socket. The runner only sees the exit
// status, which is the child's test result.
func TestExecHandsOverTheListeningSocket(t *testing.T) {
	switch os.Getenv(handoffTestEnv) {
	case "child":
		assertAdoptedByTheExec(t)
	case "parent":
		becomeTheReplacement(t)
	default:
		runHandoffSubprocess(t)
	}
}

func runHandoffSubprocess(t *testing.T) {
	t.Helper()
	resetHandoff(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestExecHandsOverTheListeningSocket$", "-test.v")
	cmd.Env = append(os.Environ(), handoffTestEnv+"=parent")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hand-off subprocess failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "handoff ok") {
		t.Fatalf("the replacement process never reported taking over the socket:\n%s", out)
	}
}

// becomeTheReplacement runs in the subprocess and never returns: execWithInheritedListener
// replaces its image with a fresh copy of this test binary.
func becomeTheReplacement(t *testing.T) {
	t.Helper()

	l, err := ServeListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Setenv(handoffAddrEnv, l.Addr().String())
	SetDrainHook(func(context.Context) { os.Setenv(handoffDrainEnv, "1") })
	// The replacement inherits this environment, so hand it the child's role or
	// it would exec itself again.
	os.Setenv(handoffTestEnv, "child")

	if !execWithInheritedListener(os.Args[0]) {
		t.Fatal("exec hand-off was refused; the port would have been closed instead")
	}
}

// assertAdoptedByTheExec runs in the replacement image.
func assertAdoptedByTheExec(t *testing.T) {
	want := os.Getenv(handoffAddrEnv)
	if want == "" {
		t.Fatal("replacement process has no address to expect")
	}
	if os.Getenv(handoffDrainEnv) != "1" {
		t.Error("the drain hook did not run before the process image was replaced")
	}
	if os.Getenv(inheritedListenerEnv) == "" {
		t.Fatal("the replacement did not receive a listening descriptor")
	}

	// A fallback bind of its own must not be what answers below.
	l, err := ServeListener("127.0.0.1:1")
	if err != nil {
		t.Fatalf("replacement could not serve: %v", err)
	}
	defer l.Close()
	if got := l.Addr().String(); got != want {
		t.Fatalf("replacement listening on %s, want the handed-over socket %s", got, want)
	}
	if os.Getenv(inheritedListenerEnv) != "" {
		t.Fatal("an inherited descriptor must be consumed on adoption")
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "inherited")
	})}
	go srv.Serve(l)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + want + "/")
	if err != nil {
		t.Fatalf("the inherited socket is not serving: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if got := string(buf[:n]); got != "inherited" {
		t.Fatalf("response = %q, want the replacement's handler", got)
	}
	fmt.Println("handoff ok")
}
