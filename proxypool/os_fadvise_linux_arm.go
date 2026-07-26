//go:build linux && arm

package proxypool

import "syscall"

// Fadvise gives the kernel access-pattern advice for fd over the byte range
// starting at offset and extending for length bytes.
// It returns [syscall.ENOSYS] on 32-bit ARM.
func Fadvise(fd int, offset int64, length int64, advice int) error {
	return syscall.ENOSYS
}
