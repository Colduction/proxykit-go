// Package lineindex locates line feeds and measures gaps between line offsets.
// It uses processor-specific vector kernels when available and portable Go elsewhere.
//
// The package shares its active backend with
// [github.com/colduction/proxykit-go/internal/structuralindex].
// [github.com/colduction/proxykit-go/internal/structuralindex.SetBackend]
// selects the kernel for both packages.
//
// See Langdale and Lemire, [Parsing Gigabytes of JSON per Second], and
// Lemire, [Iterating over set bits quickly].
//
// [Parsing Gigabytes of JSON per Second]: https://arxiv.org/abs/1902.08318
// [Iterating over set bits quickly]: https://lemire.me/blog/2018/03/08/iterating-over-set-bits-quickly-simd-edition/
package lineindex

const blockBytes = 64

// Ends stores the source's line ends in the destination in ascending order.
// It returns the number stored, the number of source bytes examined, and a
// carriage-return hint. A line end is the base offset plus the position just
// past a line feed. An empty source produces zero counts and a false hint.
//
// The hint is true when an examined line feed follows a carriage return,
// false when the examined bytes contain no carriage return, and either otherwise.
// A line feed at the source's first byte never counts as following a carriage
// return, so a caller continuing in the middle of text checks the preceding byte.
//
// [Ends] stops early only when fewer than 64 destination elements remain
// after the stored ends and fewer elements remain than unexamined source bytes.
// A destination with at least 64 elements, or at least as many elements as
// source bytes, always makes progress on a nonempty source.
// The caller can resume with the unused destination, unexamined source,
// and base offset advanced by the consumed byte count.
// Destination elements beyond the stored ends may be overwritten with unspecified values.
//
// The base offset plus the source length must fit in an unsigned 32-bit integer.
// [Ends] must not run concurrently with
// [github.com/colduction/proxykit-go/internal/structuralindex.SetBackend].
// It does not retain its arguments and does not allocate.
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

// MaxGap returns the largest difference between consecutive offsets.
// For a line start followed by line ends, this is the longest line's length,
// including its line feed. It returns zero for fewer than two offsets.
// The offsets must not decrease. [MaxGap] must not run concurrently with
// [github.com/colduction/proxykit-go/internal/structuralindex.SetBackend]
// and does not allocate.
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
