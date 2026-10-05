//go:build !unix && !windows

package blockread

import (
	"os"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// OpenSource opens the named file for reading with a [Reader].
// The sequential option requests a sequential-access hint where supported.
func OpenSource(name string, sequential bool) (*os.File, error) {
	return fileopen.Open(name, os.O_RDONLY, 0, sequential)
}
