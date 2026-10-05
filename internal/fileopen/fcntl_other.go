//go:build !darwin && !plan9

package fileopen

import "syscall"

// FcntlInt returns zero and [syscall.ENOSYS] because integer file-control
// commands are unavailable on this platform.
func FcntlInt(uintptr, int, int) (int, error) {
	return 0, syscall.ENOSYS
}
