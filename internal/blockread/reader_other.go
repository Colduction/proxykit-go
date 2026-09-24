//go:build !windows

package blockread

import "os"

// A Reader performs positional reads of one file. Init binds it to the file.
type Reader struct {
	file *os.File
}

// Init binds reader to file, which must stay open while reader is in use.
func (reader *Reader) Init(file *os.File) {
	reader.file = file
}

// ReadAt reads len(p) bytes at offset as [os.File.ReadAt] does.
func (reader *Reader) ReadAt(p []byte, offset int64) (int, error) {
	return reader.file.ReadAt(p, offset)
}

// OpenAhead returns an [Ahead] for the file of reader, or nil where the
// platform offers none, which is every platform but Windows.
func (reader *Reader) OpenAhead(int64, bool, bool) *Ahead {
	return nil
}
