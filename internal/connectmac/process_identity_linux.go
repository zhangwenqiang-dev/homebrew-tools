//go:build linux

package connectmac

import (
	"fmt"
	"os"
)

func processStartMarker(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	return parseLinuxProcStartMarker(pid, string(data))
}
