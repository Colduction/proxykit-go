//go:build (!linux || arm) && !plan9

package fileopen

import "syscall"

// Fadvise gives the kernel access-pattern advice for fd over the byte range
// starting at offset and extending for length bytes. It returns
// [syscall.ENOSYS] on these platforms, where the call is unavailable.
func Fadvise(int, int64, int64, int) error {
	return syscall.ENOSYS
}
