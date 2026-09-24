//go:build !linux && !darwin

package blockread

import "os"

// Hints asks the kernel to read ranges of a file into its cache in the
// background, with POSIX_FADV_WILLNEED on Linux and F_RDADVISE on macOS.
// Open prepares it for a file.
type Hints struct{}

// Open prepares hints for file, which must stay open while hints is in use,
// and reports whether the platform takes hints. Windows reads ahead through
// an [Ahead] instead, and the other platforms offer no asynchronous hint for
// a range.
func (*Hints) Open(*os.File) bool {
	return false
}

// Advise asks the kernel to read length bytes at offset in the background
// and reports whether it took the hint.
func (*Hints) Advise(int64, int) bool {
	return false
}
