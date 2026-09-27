//go:build linux

package blockread

import (
	"math"
	"math/bits"
	"os"
	"syscall"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// Hints asks the kernel to read ranges of a file into its cache in the
// background, with POSIX_FADV_WILLNEED on Linux and F_RDADVISE on macOS.
// Open prepares it for a file.
type Hints struct {
	raw    syscall.RawConn
	err    error
	advise func(fd uintptr)
	offset int64
	length int
}

// Open prepares hints for file, which must stay open while hints is in use,
// and reports whether the platform takes hints.
func (hints *Hints) Open(file *os.File) bool {
	// Fadvise has no 32-bit form here, so a hint would fail at once.
	if bits.UintSize == 32 {
		return false
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return false
	}
	// The callback is built once so that each hint allocates nothing, and
	// RawConn.Control keeps the descriptor valid while it runs. The kernel
	// reads at most the larger of the device's optimal request size and the
	// readahead window, 128 KiB by default, per WILLNEED request, so a range
	// takes one request per 128 KiB.
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

// Advise asks the kernel to read length bytes at offset in the background
// and reports whether it took the hint.
// It rejects negative offsets or lengths and ranges whose end exceeds the largest int64.
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
