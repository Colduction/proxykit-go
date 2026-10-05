//go:build darwin

package blockread

import (
	"math"
	"os"
	"syscall"
	"unsafe"
)

// Hints asks the kernel to read file ranges into its cache in the background.
// [Hints.Open] must succeed before [Hints.Advise] is called.
// Each request keeps the file descriptor valid while submitting advice;
// the caller keeps the file open for the lifetime of the hints.
type Hints struct {
	raw    syscall.RawConn
	advise func(fd uintptr)
	offset int64
	length int
	err    error
}

// Open binds the hints to an open file and reports whether advice can be submitted.
func (hints *Hints) Open(file *os.File) bool {
	raw, err := file.SyscallConn()
	if err != nil {
		return false
	}
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

// Advise asks the kernel to read a byte range in the background and reports
// whether the advice call succeeded. The requested length is limited to [math.MaxInt32].
func (hints *Hints) Advise(offset int64, length int) bool {
	hints.offset, hints.length, hints.err = offset, length, nil
	if err := hints.raw.Control(hints.advise); err != nil {
		return false
	}
	return hints.err == nil
}
