//go:build darwin

package blockread

import (
	"math"
	"os"
	"syscall"
	"unsafe"
)

// Hints asks the kernel to read ranges of a file into its cache in the
// background, with POSIX_FADV_WILLNEED on Linux and F_RDADVISE on macOS.
// Open prepares it for a file.
type Hints struct {
	raw    syscall.RawConn
	advise func(fd uintptr)
	offset int64
	length int
	err    error
}

// Open prepares hints for file, which must stay open while hints is in use,
// and reports whether the platform takes hints.
func (hints *Hints) Open(file *os.File) bool {
	raw, err := file.SyscallConn()
	if err != nil {
		return false
	}
	// The callback is built once so that each hint allocates nothing, and
	// RawConn.Control keeps the descriptor valid while it runs. F_RDADVISE
	// starts an asynchronous read of the range into the unified buffer cache.
	hints.raw = raw
	hints.advise = func(fd uintptr) {
		advisory := syscall.Radvisory_t{
			Offset: hints.offset,
			Count:  int32(min(hints.length, math.MaxInt32)),
		}
		_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_RDADVISE, uintptr(unsafe.Pointer(&advisory)))
		if errno != 0 {
			hints.err = errno
		}
	}
	return true
}

// Advise asks the kernel to read length bytes at offset in the background
// and reports whether it took the hint.
func (hints *Hints) Advise(offset int64, length int) bool {
	hints.offset, hints.length, hints.err = offset, length, nil
	if err := hints.raw.Control(hints.advise); err != nil {
		return false
	}
	return hints.err == nil
}
