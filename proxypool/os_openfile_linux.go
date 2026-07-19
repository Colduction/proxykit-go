//go:build linux

package proxypool

import (
	"os"
	"syscall"
)

func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	advice := 0x1 // unix.FADV_RANDOM
	if mode == ModeSequential {
		advice = 0x2 // unix.MADV_SEQUENTIAL
	}
	if err := withFD(file, func(fd uintptr) error {
		return Fadvise(int(file.Fd()), 0, 0, advice)
	}); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func Fadvise(fd int, offset int64, length int64, advice int) (err error) {
	_, _, e1 := syscall.Syscall6(syscall.SYS_FADVISE64, uintptr(fd), uintptr(offset), uintptr(length), uintptr(advice), 0, 0)
	if e1 != 0 {
		err = e1
	}
	return
}
