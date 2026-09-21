//go:build (!amd64 && !arm64) || (amd64 && netbsd) || purego

package structuralindex

// Build fills ix with the bitmaps of s and reports whether it did.
// It always reports false in a build without a vector kernel,
// leaving ix unchanged.
func (ix *Index) Build(s string) bool {
	return false
}
