//go:build linux && !arm

package proxypool

import (
	"syscall"
	"unsafe"
)

// Fadvise gives the kernel access-pattern advice for fd over the byte range
// starting at offset and extending for length bytes.
// It returns [syscall.ENOSYS] on 32-bit platforms.
func Fadvise(fd int, offset int64, length int64, advice int) error {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return syscall.ENOSYS
	}
	_, _, errno := syscall.Syscall6(
		syscall.SYS_FADVISE64,
		uintptr(fd),
		uintptr(offset),
		uintptr(length),
		uintptr(advice),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
