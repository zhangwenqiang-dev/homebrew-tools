//go:build !darwin && !linux

package connectmac

func syncDirectory(string) error {
	return nil
}
