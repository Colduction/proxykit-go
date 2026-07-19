//go:build windows

package proxypool

import (
	"os"
	"syscall"
)

func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	flags := flag | syscall.FILE_FLAG_OVERLAPPED
	if mode == ModeSequential {
		flags |= 0x08000000 // windows.O_FILE_FLAG_SEQUENTIAL_SCAN
	} else {
		flags |= 0x10000000 // windows.O_FILE_FLAG_RANDOM_ACCESS
	}
	return os.OpenFile(name, flags, perm)
}
