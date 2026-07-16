//go:build darwin

package connectmac

func terminateVerifiedProcess(state State, verify func(State) error, stop func(int) error) error {
	if err := verify(state); err != nil {
		return err
	}
	// Darwin has no pidfd equivalent, so a minimal exit/PID-reuse window remains before kill.
	return stop(state.PID)
}
