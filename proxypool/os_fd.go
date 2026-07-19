//go:build linux || darwin

package proxypool

import "os"

func withFD(file *os.File, fn func(fd uintptr) error) error {
	if file == nil {
		return os.ErrInvalid
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var operationErr error
	if err := raw.Control(func(fd uintptr) {
		operationErr = fn(fd)
	}); err != nil {
		return err
	}
	return operationErr
}
