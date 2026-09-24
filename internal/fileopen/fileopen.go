// Package fileopen opens files with the access hints of their platform and
// gives access to their descriptors, for packages that read files with
// knowledge of their access pattern.
package fileopen

import "os"

// Open opens name with flag and perm as [os.OpenFile] does and, when
// sequential is true, tells the platform that the file will be read from
// start to end: Linux gets POSIX_FADV_SEQUENTIAL and macOS F_RDAHEAD. On
// Windows the handle is opened for overlapped I/O, with
// FILE_FLAG_SEQUENTIAL_SCAN when sequential is true. A hint the platform
// rejects does not fail the open.
func Open(name string, flag int, perm os.FileMode, sequential bool) (*os.File, error) {
	return open(name, flag, perm, sequential)
}

// WithFD calls fn with the descriptor or handle of file, which stays valid
// while fn runs, and returns the error of fn or of reaching the descriptor.
func WithFD(file *os.File, fn func(fd uintptr) error) error {
	if file == nil {
		return os.ErrInvalid
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var operationErr error
	if err := raw.Control(func(fd uintptr) {
		operationErr = fn(fd)
	}); err != nil {
		return err
	}
	return operationErr
}
