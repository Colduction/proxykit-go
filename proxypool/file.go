package proxypool

import (
	"os"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenFile opens name using flag and perm, with platform-specific access
// behavior selected by mode: in sequential mode Linux gets
// POSIX_FADV_SEQUENTIAL, macOS F_RDAHEAD, and Windows
// FILE_FLAG_SEQUENTIAL_SCAN. On Windows the handle is opened for overlapped
// I/O in both modes.
func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	return fileopen.Open(name, flag, perm, mode == ModeSequential)
}

// Fadvise gives the kernel access-pattern advice for fd over the byte range
// starting at offset and extending for length bytes. It returns
// syscall.ENOSYS where the call is unavailable: on 32-bit platforms and on
// every platform but Linux. Plan 9 returns [errors.ErrUnsupported] instead.
func Fadvise(fd int, offset int64, length int64, advice int) error {
	return fileopen.Fadvise(fd, offset, length, advice)
}

// FcntlInt invokes fcntl on fd with integer command cmd and argument arg and
// returns the result and the error the kernel reports. It returns
// syscall.ENOSYS on every platform but macOS; Plan 9 returns
// [errors.ErrUnsupported] instead.
func FcntlInt(fd uintptr, cmd, arg int) (int, error) {
	return fileopen.FcntlInt(fd, cmd, arg)
}
