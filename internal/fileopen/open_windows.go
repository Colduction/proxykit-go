//go:build windows

package fileopen

import (
	"os"
	"syscall"
)

// FlagSequentialScan is FILE_FLAG_SEQUENTIAL_SCAN, which [os.OpenFile]
// passes to CreateFile on Windows.
const FlagSequentialScan = 0x08000000

func open(name string, flag int, perm os.FileMode, sequential bool) (*os.File, error) {
	flag |= syscall.FILE_FLAG_OVERLAPPED
	if sequential {
		flag |= FlagSequentialScan
	}
	return os.OpenFile(name, flag, perm)
}
