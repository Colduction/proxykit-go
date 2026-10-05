// Package fileopen opens files with the access hints of their platform and
// gives access to their descriptors, for packages that read files with
// knowledge of their access pattern.
package fileopen

import "os"

// Open opens the named file with flags and permissions as [os.OpenFile] does.
// The sequential option tells supported platforms that the file will be read
// from start to end. Windows opens the handle for overlapped I/O.
// A rejected access hint does not fail the open.
func Open(name string, flag int, perm os.FileMode, sequential bool) (*os.File, error) {
	return open(name, flag, perm, sequential)
}

// WithFD calls the callback with the file's descriptor or handle, which stays
// valid until the callback returns. The callback must not retain the descriptor.
// It returns the callback's error or the error from accessing the descriptor.
// A nil file returns [os.ErrInvalid]; the callback must be non-nil.
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
