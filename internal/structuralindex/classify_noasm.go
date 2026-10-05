//go:build (!amd64 && !arm64) || (amd64 && netbsd) || purego

package structuralindex

// Build fills the index with the string's byte-class bitmaps and reports whether it did.
// It always reports false in a build without a vector kernel, leaving the index unchanged.
func (ix *Index) Build(s string) bool {
	return false
}
