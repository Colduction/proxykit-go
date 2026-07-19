//go:build darwin

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
	var readAhead int // Closest Darwin equivalent to random access.
	if mode == ModeSequential {
		readAhead = 1
	}
	if err := withFD(file, func(fd uintptr) error {
		_, err := FcntlInt(
			fd,
			syscall.F_RDAHEAD,
			readAhead,
		)
		return err
	}); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func FcntlInt(fd uintptr, cmd, arg int) (int, error) {
	return fcntl(int(fd), cmd, arg)
}

func fcntl(fd int, cmd, arg int) (int, error) {
	valptr, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg))
	var err error
	if errno != 0 {
		err = errno
	}
	return int(valptr), err
}
