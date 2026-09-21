//go:build ((amd64 && !netbsd) || arm64) && !purego

package structuralindex

import "unsafe"

// Build fills ix with the bitmaps of s and reports whether it did.
// It reports false, leaving ix unchanged, when s is empty or longer than
// [MaxLen] or when the active backend is [Portable].
// It does not retain s and does not allocate.
func (ix *Index) Build(s string) bool {
	if active == Portable || uint(len(s)-1) >= MaxLen {
		return false
	}
	classify(unsafe.StringData(s), len(s), &ix.bitmaps)
	return true
}
