//go:build windows

package updater

import (
	"errors"
	"net"
)

// Windows has no portable way to hand a listening socket to a replacement
// process, so the descriptor is never recorded and RestartSelf takes its
// spawn-a-child path instead. The stubs keep the unix implementation out of the
// build without a second code path in the portable code.

func recordHandoffFD(int) {}

func listenerFromFD(int) (net.Listener, error) {
	return nil, errors.New("listener hand-off is not supported on windows")
}

func clearCloseOnExec(uintptr) error {
	return errors.New("listener hand-off is not supported on windows")
}

func execWithInheritedListener(string) bool { return false }
