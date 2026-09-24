//go:build !purego && !netbsd

package lineindex

import "github.com/colduction/proxykit-go/internal/structuralindex"

func vectorized() bool {
	// It reports whether the active backend has a vector kernel.
	return structuralindex.ActiveBackend() != structuralindex.Portable
}

func endsBlocks(dst []uint32, src []byte, base uint32) (written, consumed int, cr bool) {
	switch structuralindex.ActiveBackend() {
	case structuralindex.AVX512:
		return endsAVX512(&dst[0], len(dst), &src[0], len(src), base)
	case structuralindex.AVX2:
		return endsAVX2(&dst[0], len(dst), &src[0], len(src), base)
	}
	return endsScalar(dst, src, base)
}

func maxGapLanes(offsets []uint32) (largest uint32, gaps int) {
	gaps = (len(offsets) - 1) &^ 7
	if gaps == 0 || !vectorized() {
		return maxGapWords(offsets)
	}
	return maxGapAVX2(&offsets[0], gaps), gaps
}

//go:noescape
func endsAVX512(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)

//go:noescape
func endsAVX2(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)

//go:noescape
func maxGapAVX2(offsets *uint32, gaps int) uint32
