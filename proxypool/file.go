package proxypool

import (
	"os"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenFile opens the named file with the requested flags and permissions.
// Sequential iteration selects sequential-access advice or flags on Linux,
// macOS, and Windows. On Windows the handle supports overlapped I/O in both modes.
// An unsupported mode returns an error without opening or changing the file.
func OpenFile(name string, flag int, perm os.FileMode, mode Mode) (*os.File, error) {
	if err := mode.Valid(); err != nil {
		return nil, err
	}
	return fileopen.Open(name, flag, perm, mode == ModeSequential)
}

// Fadvise gives the kernel access-pattern advice for a file descriptor over the
// byte range beginning at the requested offset and extending for the requested length.
// It returns [syscall.ENOSYS] where the call is unavailable: on 32-bit platforms and on
// every platform but Linux. Plan 9 returns [errors.ErrUnsupported] instead.
func Fadvise(fd int, offset int64, length int64, advice int) error {
	return fileopen.Fadvise(fd, offset, length, advice)
}

// FcntlInt invokes the file-control system call with a file descriptor, integer
// command, and integer argument and returns the kernel result and error.
// It returns [syscall.ENOSYS] on every platform but macOS; Plan 9 returns
// [errors.ErrUnsupported] instead.
func FcntlInt(fd uintptr, cmd, arg int) (int, error) {
	return fileopen.FcntlInt(fd, cmd, arg)
}
