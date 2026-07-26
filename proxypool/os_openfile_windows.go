//go:build windows

package proxypool

import (
	"os"
	"syscall"
)

const fileFlagSequentialScan = 0x08000000

// OpenFile opens name using flag and perm, with platform-specific access
// behavior selected by mode.
func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	flags := flag | syscall.FILE_FLAG_OVERLAPPED
	if mode == ModeSequential {
		flags |= fileFlagSequentialScan
	}
	return os.OpenFile(name, flags, perm)
}
