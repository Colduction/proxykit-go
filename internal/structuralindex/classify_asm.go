//go:build ((amd64 && !netbsd) || arm64) && !purego

package structuralindex

import "unsafe"

// Build fills the index with the string's byte-class bitmaps and reports whether it did.
// It reports false, leaving the index unchanged, when the string is empty or longer than
// [MaxLen] or when the active backend is [Portable].
// It does not retain the string and does not allocate.
// It must not run concurrently with [SetBackend] or another access to the same index.
func (ix *Index) Build(s string) bool {
	if active == Portable || uint(len(s)-1) >= MaxLen {
		return false
	}
	classify(unsafe.StringData(s), len(s), &ix.bitmaps)
	return true
}
