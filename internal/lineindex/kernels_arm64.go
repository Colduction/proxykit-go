//go:build !purego

package lineindex

import "github.com/colduction/proxykit-go/internal/structuralindex"

func vectorized() bool {
	// It reports whether the active backend has a vector kernel.
	return structuralindex.ActiveBackend() == structuralindex.NEON
}

func endsBlocks(dst []uint32, src []byte, base uint32) (written, consumed int, cr bool) {
	if vectorized() {
		return endsNEON(&dst[0], len(dst), &src[0], len(src), base)
	}
	return endsScalar(dst, src, base)
}

func maxGapLanes(offsets []uint32) (largest uint32, gaps int) {
	gaps = (len(offsets) - 1) &^ 3
	if gaps == 0 || !vectorized() {
		return maxGapWords(offsets)
	}
	return maxGapNEON(&offsets[0], gaps), gaps
}

//go:noescape
func endsNEON(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)

//go:noescape
func maxGapNEON(offsets *uint32, gaps int) uint32
