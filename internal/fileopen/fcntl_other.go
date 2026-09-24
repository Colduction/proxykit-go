//go:build !darwin && !plan9

package fileopen

import "syscall"

// FcntlInt invokes fcntl on fd with integer command cmd and argument arg and
// returns the result and the error the kernel reports. It returns
// [syscall.ENOSYS] on these platforms, where the call is unavailable.
func FcntlInt(uintptr, int, int) (int, error) {
	return 0, syscall.ENOSYS
}
