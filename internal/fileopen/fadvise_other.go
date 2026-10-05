//go:build (!linux || arm) && !plan9

package fileopen

import "syscall"

// Fadvise returns [syscall.ENOSYS] because access-pattern advice is unavailable
// on this platform.
func Fadvise(int, int64, int64, int) error {
	return syscall.ENOSYS
}
