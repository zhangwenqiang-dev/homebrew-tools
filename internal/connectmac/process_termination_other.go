//go:build !darwin && !linux

package connectmac

func terminateVerifiedProcess(state State, verify func(State) error, stop func(int) error) error {
	if err := verify(state); err != nil {
		return err
	}
	return stop(state.PID)
}
