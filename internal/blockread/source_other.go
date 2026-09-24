//go:build !unix && !windows

package blockread

import (
	"os"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenSource opens name for reading with a [Reader], with the platform's
// hint of sequential reading when sequential is true.
func OpenSource(name string, sequential bool) (*os.File, error) {
	return fileopen.Open(name, os.O_RDONLY, 0, sequential)
}
