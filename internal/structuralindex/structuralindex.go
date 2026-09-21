// Package structuralindex classifies the bytes of short ASCII text so that
// delimiters are located and whole ranges are validated with a few word
// operations instead of one branch per byte.
//
// It has two tiers.
// An [Index] is a structural index in the sense of Mison and simdjson:
// one 64-bit bitmap per byte class, bit i describing byte i,
// filled by a vector kernel written in Go assembly
// (AVX-512 or AVX2 on amd64, NEON on arm64)
// and queried with [math/bits] operations.
// [IsHostName] and [IsPrintable] are the portable tier:
// they test eight bytes per 64-bit word (SIMD within a register),
// need no assembly, and have no 64-byte input limit,
// though IsHostName leaves a name over 63 bytes to its caller.
// The purego build tag selects the portable tier alone,
// and so does netbsd on amd64, whose kernel has corrupted AVX state
// across signals.
//
// The package holds mechanism only.
// Which byte ranges must satisfy which class is the caller's policy.
//
// # References
//
//   - Y. Li, N. R. Katsipoulakis, B. Chandramouli, J. Goldstein, D. Kossmann,
//     "Mison: A Fast JSON Parser for Data Analytics",
//     PVLDB 10(10), 2017: structural index of per-character bitmaps.
//   - G. Langdale, D. Lemire, "Parsing Gigabytes of JSON per Second",
//     The VLDB Journal 28(6), 2019, arXiv:1902.08318:
//     vectorized classification and bitmap queries (stage 1).
//   - Y. Nizipli, D. Lemire, "Parsing Millions of URLs per Second",
//     Software: Practice and Experience 54(5), 2024, arXiv:2311.10533:
//     the same techniques applied to URL syntax.
//   - J. Keiser, D. Lemire, "Validating UTF-8 In Less Than One Instruction
//     Per Byte", Software: Practice and Experience 51(5), 2021,
//     arXiv:2010.03090: vectorized range classification.
//   - L. Lamport, "Multiple Byte Processing with Full-Word Instructions",
//     Communications of the ACM 18(8), 1975: the word-at-a-time byte tests.
//   - H. S. Warren, "Hacker's Delight", 2nd ed., 2012, section 6-1:
//     exact zero-byte detection without carries between bytes.
package structuralindex

import "math/bits"

// MaxLen is the longest text, in bytes, that an [Index] covers.
const MaxLen = 64

// A Class identifies one byte class of an [Index].
type Class uint8

// The byte classes of an [Index].
const (
	// Colon is the class of ':'.
	Colon Class = iota
	// At is the class of '@'.
	At
	// Dot is the class of '.'.
	Dot
	// Hyphen is the class of '-'.
	Hyphen
	// Alnum is the class of '0' to '9', 'A' to 'Z', and 'a' to 'z'.
	Alnum
	// Unprintable is the class of bytes below 0x20 or above 0x7E.
	Unprintable
	classCount
)

// ClassOf returns the [Class] that holds exactly the byte b
// and reports whether there is one.
func ClassOf(b byte) (Class, bool) {
	switch b {
	case ':':
		return Colon, true
	case '@':
		return At, true
	case '.':
		return Dot, true
	case '-':
		return Hyphen, true
	default:
		return 0, false
	}
}

// An Index holds one bitmap per [Class] for a text of at most [MaxLen] bytes.
// Bit i of a bitmap is set when byte i of the text belongs to the class;
// bits at and above the text length are clear.
// The zero Index describes the empty text.
// An Index holds no reference to the text.
type Index struct {
	bitmaps [classCount]uint64
}

// Bitmap returns the bitmap of class c.
func (ix *Index) Bitmap(c Class) uint64 {
	return ix.bitmaps[c]
}

// Next returns the position of the first byte of class c at or after from,
// or -1 if there is none. from must be in the range 0 to [MaxLen].
func (ix *Index) Next(c Class, from int) int {
	remaining := ix.bitmaps[c] &^ LowMask(from)
	if remaining == 0 {
		return -1
	}
	return bits.TrailingZeros64(remaining)
}

// LowMask returns a bitmap with bits 0 to n-1 set.
// n must be in the range 0 to [MaxLen].
func LowMask(n int) uint64 {
	return 1<<uint(n) - 1
}

// Range returns a bitmap with bits start to end-1 set.
// It requires 0 <= start <= end <= [MaxLen].
func Range(start, end int) uint64 {
	return LowMask(end) &^ LowMask(start)
}
