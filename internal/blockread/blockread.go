// Package blockread provides positional file reads, aligned buffers, and
// platform-specific read-ahead operations.
//
// A [Reader] performs positional reads, [Hints] asks the kernel to read
// ranges ahead, and an [Ahead] keeps reads in flight into storage it holds
// while they run. None of them starts a goroutine.
package blockread

import "unsafe"

const (
	// PageBytes is the page alignment of the buffers [MakeBuffer] returns and
	// of reads without buffering.
	PageBytes = 4 << 10

	// AlignedBytes is the smallest capacity that [MakeBuffer] aligns.
	AlignedBytes = 64 << 10

	// AheadDepth is the number of reads an [Ahead] keeps in flight at most.
	AheadDepth = 3
)

// MakeBuffer returns a buffer with the requested length and capacity.
// A capacity of at least [AlignedBytes] places the second byte on a page
// boundary, matching page-aligned file reads that reserve the first byte.
// [BufferBytes] includes the alignment padding.
// Invalid lengths or capacities panic as they do with slice allocation.
func MakeBuffer(length, capacity int) []byte {
	if capacity < AlignedBytes {
		return make([]byte, length, capacity)
	}
	raw := make([]byte, capacity+PageBytes-1)
	start := int((PageBytes - 1 - uintptr(unsafe.Pointer(unsafe.SliceData(raw)))) & (PageBytes - 1))
	return raw[start : start+length : start+capacity]
}

// BufferBytes returns the bytes retained by [MakeBuffer] for the requested capacity.
func BufferBytes(capacity int) int64 {
	if capacity < AlignedBytes {
		return int64(capacity)
	}
	return int64(capacity) + PageBytes - 1
}

// PageAligned reports whether the buffer starts on a page boundary, as a read
// without buffering requires. A nil buffer is aligned.
func PageAligned(p []byte) bool {
	return uintptr(unsafe.Pointer(unsafe.SliceData(p)))&(PageBytes-1) == 0
}
