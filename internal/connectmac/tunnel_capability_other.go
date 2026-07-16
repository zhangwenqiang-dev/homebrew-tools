//go:build !darwin && !linux

package connectmac

import (
	"errors"
)

func tunnelLifecyclePreflight() error {
	return errors.New("managed tunnel process identity is unsupported on this OS")
}
