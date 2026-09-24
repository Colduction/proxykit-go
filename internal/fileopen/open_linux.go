//go:build linux

package fileopen

import "os"

const posixFadvSequential = 2

func open(name string, flag int, perm os.FileMode, sequential bool) (*os.File, error) {
	file, err := os.OpenFile(name, flag, perm)
	if err != nil || !sequential {
		return file, err
	}
	_ = WithFD(file, func(fd uintptr) error {
		return Fadvise(int(fd), 0, 0, posixFadvSequential)
	})
	return file, nil
}
