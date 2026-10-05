//go:build darwin

package fileopen

import "syscall"

// FcntlInt issues a file-control command with an integer argument for a file
// descriptor and returns the result and error reported by the kernel.
func FcntlInt(fd uintptr, cmd, arg int) (int, error) {
	value, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(cmd), uintptr(arg))
	if errno != 0 {
		return int(value), errno
	}
	return int(value), nil
}
