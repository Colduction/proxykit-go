//go:build !windows

package blockread

import "os"

// A Reader performs positional reads of one file.
// Initialize it with [Reader.Init] before use.
type Reader struct {
	file *os.File
}

// Init binds the reader to an open file, which must remain open while the reader is in use.
func (reader *Reader) Init(file *os.File) {
	reader.file = file
}

// ReadAt reads into the buffer at the byte offset as [os.File.ReadAt] does.
func (reader *Reader) ReadAt(p []byte, offset int64) (int, error) {
	return reader.file.ReadAt(p, offset)
}

// OpenAhead returns nil because this platform has no [Ahead] implementation.
func (reader *Reader) OpenAhead(int64, bool, bool) *Ahead {
	return nil
}
