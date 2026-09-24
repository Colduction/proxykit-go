//go:build unix

package blockread

import (
	"os"
	"syscall"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenSource opens name for reading with a [Reader], with the platform's
// hint of sequential reading when sequential is true.
// It does not wait for a FIFO writer, so callers can reject nonregular files
// after inspecting the opened descriptor.
func OpenSource(name string, sequential bool) (*os.File, error) {
	return fileopen.Open(name, os.O_RDONLY|syscall.O_NONBLOCK, 0, sequential)
}
