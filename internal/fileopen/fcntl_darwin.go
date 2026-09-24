//go:build darwin

package fileopen

import "syscall"

// FcntlInt invokes fcntl on fd with integer command cmd and argument arg and
// returns the result and the error the kernel reports.
func FcntlInt(fd uintptr, cmd, arg int) (int, error) {
	value, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(cmd), uintptr(arg))
	if errno != 0 {
		return int(value), errno
	}
	return int(value), nil
}
