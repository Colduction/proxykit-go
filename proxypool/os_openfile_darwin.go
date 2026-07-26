//go:build darwin

package proxypool

import (
	"os"
	"syscall"
)

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
		_, err := FcntlInt(
			fd,
			syscall.F_RDAHEAD,
			1,
		)
		return err
	})
	return file, nil
}

// FcntlInt invokes fcntl on fd with integer command cmd and argument arg.
// It returns the result and error reported by the kernel.
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
