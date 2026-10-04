//go:build !windows

package updater

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"

	"9router/proxy/internal/log"
)

// recordHandoffFD remembers the socket this process listens on so RestartSelf
// can hand it to its replacement.
func recordHandoffFD(fd int) {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	handoffFDNum = fd
}

// listenerFromFD adopts a socket left open by the previous process image.
// FileListener duplicates the descriptor into the runtime's poller, so our own
// handle to it is only a means to that end and is closed here.
func listenerFromFD(fd int) (net.Listener, error) {
	f := os.NewFile(uintptr(fd), "inherited-listener")
	if f == nil {
		return nil, fmt.Errorf("descriptor %d is not a file", fd)
	}
	defer f.Close()
	return net.FileListener(f)
}

// clearCloseOnExec drops FD_CLOEXEC from fd so the socket survives an exec. Go
// opens every socket close-on-exec by default, which is exactly what would
// snap the port shut in the middle of the restart.
func clearCloseOnExec(fd uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_SETFD), 0); errno != 0 {
		return errno
	}
	return nil
}

// execWithInheritedListener replaces this process image — same PID, so a
// container whose PID 1 is the gateway keeps running instead of restarting —
// with the binary now on disk, handing it the listening socket. Reports whether
// it took over the process; false means the caller should fall back to spawning
// a child.
func execWithInheritedListener(execPath string) bool {
	fd := currentHandoffFD()
	if fd < 0 {
		return false
	}

	// Drain first: the exec discards this address space, so a response still
	// being written would be cut mid-body.
	if drain := registeredDrain(); drain != nil {
		ctx, cancel := context.WithTimeout(context.Background(), restartDrainTimeout)
		drain(ctx)
		cancel()
	}

	if err := clearCloseOnExec(uintptr(fd)); err != nil {
		log.Error("updater", "cannot keep the listening socket across the restart", "fd", fd, "error", err)
		return false
	}

	// Setenv rather than appending: a stale descriptor left by an earlier
	// generation would otherwise sit in the environment as a second entry for
	// the same key, and which one the child reads is platform-dependent.
	os.Setenv(inheritedListenerEnv, strconv.Itoa(fd))

	log.Info("updater", "restarting in place, listening socket handed to the new version",
		"fd", fd, "binary", execPath)

	// exec only returns on failure, and then nothing has been replaced: an
	// update that cannot be applied must not take the service down.
	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Error("updater", "in-place restart failed, keeping the running process", "error", err)
		return false
	}
	return true
}
