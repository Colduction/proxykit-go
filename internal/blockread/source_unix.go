//go:build unix

package blockread

import (
	"os"
	"syscall"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenSource opens the named file for reading with a [Reader].
// The sequential option requests the platform's sequential-access hint.
// It does not wait for a FIFO writer, so callers can reject nonregular files
// after inspecting the opened descriptor.
func OpenSource(name string, sequential bool) (*os.File, error) {
	return fileopen.Open(name, os.O_RDONLY|syscall.O_NONBLOCK, 0, sequential)
}
