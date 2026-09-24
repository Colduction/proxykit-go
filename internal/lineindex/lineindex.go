// Package lineindex finds the line feeds of text and writes the offset just
// past each one, 64 bytes at a time with AVX-512 or AVX2 on amd64 and NEON on
// arm64, and in pure Go everywhere else.
//
// The vector kernels follow the structural indexing of simdjson: a vector
// compare turns each block of 64 bytes into a 64-bit mask, and the positions of
// its set bits are stored with a fixed number of unconditional stores that the
// count of set bits then accepts, which avoids a branch per line feed. The pure
// Go kernel tests eight bytes per machine word while lines are short and
// searches line by line with bytes.IndexByte while they are long, whichever
// costs less for the text at hand. The package shares the backend of
// internal/structuralindex, so one call of structuralindex.SetBackend selects
// the kernel of both.
//
// See Langdale and Lemire, [Parsing Gigabytes of JSON per Second], and
// Lemire, [Iterating over set bits quickly].
//
// [Parsing Gigabytes of JSON per Second]: https://arxiv.org/abs/1902.08318
// [Iterating over set bits quickly]: https://lemire.me/blog/2018/03/08/iterating-over-set-bits-quickly-simd-edition/
package lineindex

const blockBytes = 64

// Ends stores the line ends of src in dst in ascending order and returns the
// number it stored and the number of bytes of src it examined. A line end is
// base plus the index just past a line feed.
//
// The cr result is a hint for trimming carriage returns: it is true when a line
// feed that Ends found follows a carriage return in src, false when the
// examined bytes hold no carriage return, and either otherwise. A line feed at
// src[0] never counts as following one, so a caller that continues in the
// middle of text tests the byte before src itself.
//
// Ends stores without a check per line feed, so it needs room. It stops early
// only when fewer than 64 elements of dst remain past the ones it stored and
// fewer remain than bytes of src remain to examine. A caller then continues with
// Ends(dst[written:], src[consumed:], base+uint32(consumed)) after making room.
// A dst of at least 64 elements, or of at least len(src) elements, always makes
// progress. Elements of dst at index written and beyond may be overwritten with
// unspecified values.
//
// base+len(src) must not exceed the largest uint32. Ends must not run
// concurrently with structuralindex.SetBackend. It does not retain its
// arguments and does not allocate.
func Ends(dst []uint32, src []byte, base uint32) (written, consumed int, cr bool) {
	if !vectorized() {
		return endsScalar(dst, src, base)
	}
	if whole := len(src) &^ (blockBytes - 1); whole > 0 && len(dst) >= blockBytes {
		written, consumed, cr = endsBlocks(dst, src[:whole], base)
	}
	rest := len(src) - consumed
	if rest == 0 || rest >= blockBytes || len(dst)-written < rest {
		return written, consumed, cr
	}
	var (
		block [blockBytes]byte
		ends  [blockBytes]uint32
	)
	copy(block[:], src[consumed:])
	n, _, tailCR := endsBlocks(ends[:], block[:], base+uint32(consumed))
	copy(dst[written:], ends[:n])
	return written + n, len(src), cr || tailCR
}

// MaxGap returns the largest difference between consecutive elements of
// offsets, which for a line start followed by the ends of lines is the length
// of the longest of those lines, including its line feed. It returns 0 for
// fewer than two elements. The elements must not decrease. MaxGap must not run
// concurrently with structuralindex.SetBackend and does not allocate.
func MaxGap(offsets []uint32) uint32 {
	if len(offsets) < 2 {
		return 0
	}
	largest, gaps := maxGapLanes(offsets)
	for i := gaps + 1; i < len(offsets); i++ {
		largest = max(largest, offsets[i]-offsets[i-1])
	}
	return largest
}
