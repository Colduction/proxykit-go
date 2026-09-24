// Package blockread reads a file one block at a time as fast as its platform
// allows: into buffers that share their page offset with the file, which
// kernel copy routines handle fastest; through ReadFile on a synchronous
// handle on Windows, which serves an uncached file faster than an overlapped
// one; with hints that start the kernel reading ahead on Linux and macOS; and
// with reads kept in flight on Windows, without buffering when the cache
// could not hold the file.
//
// A [Reader] performs positional reads, [Hints] asks the kernel to read
// ranges ahead, and an [Ahead] keeps reads in flight into storage it holds
// while they run. None of them starts a goroutine.
package blockread

import "unsafe"

const (
	// PageBytes is the page alignment of the buffers MakeBuffer returns and
	// of reads without buffering.
	PageBytes = 4 << 10

	// AlignedBytes is the smallest capacity that MakeBuffer aligns.
	AlignedBytes = 64 << 10

	// AheadDepth is the number of reads an [Ahead] keeps in flight at most.
	AheadDepth = 3
)

// MakeBuffer returns a buffer of length and capacity. A buffer of at least
// [AlignedBytes] starts one byte before a page boundary, so that a block read
// into buffer[1:] from a page-aligned file offset copies between addresses at
// the same offset in their pages, and so does a read into buffer[0:] from one
// byte earlier. The padding costs less than one page, which [BufferBytes]
// counts.
func MakeBuffer(length, capacity int) []byte {
	if capacity < AlignedBytes {
		return make([]byte, length, capacity)
	}
	raw := make([]byte, capacity+PageBytes-1)
	start := int((PageBytes - 1 - uintptr(unsafe.Pointer(unsafe.SliceData(raw)))) & (PageBytes - 1))
	return raw[start : start+length : start+capacity]
}

// BufferBytes returns the memory that [MakeBuffer] retains for capacity.
func BufferBytes(capacity int) int64 {
	if capacity < AlignedBytes {
		return int64(capacity)
	}
	return int64(capacity) + PageBytes - 1
}

// PageAligned reports whether p starts on a page boundary, as a read
// without buffering requires.
func PageAligned(p []byte) bool {
	return uintptr(unsafe.Pointer(unsafe.SliceData(p)))&(PageBytes-1) == 0
}
