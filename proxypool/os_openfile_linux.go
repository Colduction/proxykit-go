//go:build linux

package proxypool

import "os"

const posixFadvSequential = 2

// OpenFile opens name using flag and perm, with platform-specific access
// behavior selected by mode.
func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	file, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if mode != ModeSequential {
		return file, nil
	}
	_ = withFD(file, func(fd uintptr) error {
		return Fadvise(int(fd), 0, 0, posixFadvSequential)
	})
	return file, nil
}
