//go:build darwin

package fileopen

import (
	"os"
	"syscall"
)

func open(name string, flag int, perm os.FileMode, sequential bool) (*os.File, error) {
	file, err := os.OpenFile(name, flag, perm)
	if err != nil || !sequential {
		return file, err
	}
	_ = WithFD(file, func(fd uintptr) error {
		_, err := FcntlInt(fd, syscall.F_RDAHEAD, 1)
		return err
	})
	return file, nil
}
