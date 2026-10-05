//go:build linux

package blockread

import (
	"math"
	"math/bits"
	"os"
	"syscall"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// Hints asks the kernel to read file ranges into its cache in the background.
// [Hints.Open] must succeed before [Hints.Advise] is called.
// Each request keeps the file descriptor valid while submitting advice;
// the caller keeps the file open for the lifetime of the hints.
type Hints struct {
	raw    syscall.RawConn
	err    error
	advise func(fd uintptr)
	offset int64
	length int
}

// Open binds the hints to an open file and reports whether advice can be submitted.
// It returns false on 32-bit Linux.
func (hints *Hints) Open(file *os.File) bool {
	if bits.UintSize == 32 {
		return false
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return false
	}
	const (
		posixFadvWillNeed = 3
		chunkBytes        = 128 << 10
	)
	hints.raw = raw
	hints.advise = func(fd uintptr) {
		end := hints.offset + int64(hints.length)
		for offset := hints.offset; offset < end && hints.err == nil; {
			length := min(chunkBytes, end-offset)
			hints.err = fileopen.Fadvise(int(fd), offset, length, posixFadvWillNeed)
			offset += length
		}
	}
	return true
}

// Advise asks the kernel to read the requested byte range in the background
// and reports whether all advice calls succeeded.
// It submits the range in chunks of at most 128 KiB.
// It rejects negative offsets or lengths and ranges whose end exceeds [math.MaxInt64].
func (hints *Hints) Advise(offset int64, length int) bool {
	if offset < 0 || length < 0 || int64(length) > math.MaxInt64-offset {
		return false
	}
	hints.offset, hints.length, hints.err = offset, length, nil
	if err := hints.raw.Control(hints.advise); err != nil {
		return false
	}
	return hints.err == nil
}
