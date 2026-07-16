//go:build darwin

package connectmac

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processStartMarker(pid int) (string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	start := process.Proc.P_starttime
	if start.Sec == 0 && start.Usec == 0 {
		return "", fmt.Errorf("pid %d has an empty kernel start time", pid)
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), nil
}
