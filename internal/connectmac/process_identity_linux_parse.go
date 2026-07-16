package connectmac

import (
	"fmt"
	"strconv"
	"strings"
)

func parseLinuxProcStartMarker(pid int, line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("pid %d has empty proc stat", pid)
	}
	commandEnd := strings.LastIndex(line, ")")
	if commandEnd < 0 {
		return "", fmt.Errorf("pid %d has malformed proc stat", pid)
	}
	fields := strings.Fields(line[commandEnd+1:])
	const startTimeIndexAfterCommand = 19
	if len(fields) <= startTimeIndexAfterCommand {
		return "", fmt.Errorf("pid %d proc stat has no start time", pid)
	}
	startTicks, err := strconv.ParseUint(fields[startTimeIndexAfterCommand], 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse pid %d start time: %w", pid, err)
	}
	return strconv.FormatUint(startTicks, 10), nil
}
