//go:build linux

package connectmac

import (
	"errors"

	"golang.org/x/sys/unix"
)

func terminateVerifiedProcess(state State, verify func(State) error, stop func(int) error) error {
	pidfd, err := unix.PidfdOpen(state.PID, 0)
	if err == nil {
		defer unix.Close(pidfd)
		if err := verify(state); err != nil {
			return err
		}
		return unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
	}
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	if err := verify(state); err != nil {
		return err
	}
	return stop(state.PID)
}
