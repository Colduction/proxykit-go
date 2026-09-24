//go:build (!amd64 && !arm64) || (amd64 && netbsd) || purego

package lineindex

func vectorized() bool {
	// It reports whether the active backend has a vector kernel.
	return false
}

func endsBlocks(dst []uint32, src []byte, base uint32) (written, consumed int, cr bool) {
	return endsScalar(dst, src, base)
}

func maxGapLanes(offsets []uint32) (largest uint32, gaps int) {
	return maxGapWords(offsets)
}
