//go:build !darwin && !linux

package connectmac

import (
	"errors"
)

func processStartMarker(pid int) (string, error) {
	return "", errors.New("stable process start identity is unsupported on this OS")
}
