package structuralindex

// Word constants for tests on eight bytes at a time.
const (
	// EachByte holds 0x01 in every byte; a multiple of it holds that value
	// in every byte.
	EachByte = 0x0101010101010101
	// HighBits holds bit 7 of every byte, where the word tests leave a result.
	HighBits = 0x80 * EachByte
	// LowSeven holds the seven low bits of every byte.
	LowSeven = 0x7f * EachByte
)

const maxHostNameLen = 63

// IsHostName reports whether s is a relative DNS host name that needs no
// further syntax check: 1 to 63 bytes of ASCII letters, digits, hyphens, and
// dots, in which no label is empty and none begins or ends with a hyphen.
//
// It is a sufficient test, not a complete one.
// It reports false for a valid name that is longer than 63 bytes
// or ends with a root dot, and it does not apply the rule that a name
// whose final label is numeric must be an IPv4 address,
// so the caller decides those cases.
func IsHostName(s string) bool {
	// Within maxHostNameLen bytes no label can exceed the 63-byte DNS limit,
	// so label lengths need no check of their own.
	n := len(s)
	if uint(n-1) >= maxHostNameLen {
		return false
	}
	if n <= 8 {
		return invalidNameBytes(loadPadded(s), 0x80|0x80<<(uint(n-1)*8))&HighBits == 0
	}
	// Consecutive words share one byte, so every adjacent pair of bytes lies
	// inside some word and no state passes from word to word. The final word
	// ends at the last byte and overlaps as much as it needs to.
	invalid := invalidNameBytes(Load64(s, 0), 0x80)
	for i := 7; i < n-8; i += 7 {
		invalid |= invalidNameBytes(Load64(s, i), 0)
	}
	invalid |= invalidNameBytes(Load64(s, n-8), 0x80<<56)
	return invalid&HighBits == 0
}

// IsPrintable reports whether every byte of s is printable ASCII,
// 0x20 to 0x7E. It reports true for the empty string.
func IsPrintable(s string) bool {
	n := len(s)
	if n < 8 {
		return UnprintableBytes(loadPadded(s))&HighBits == 0
	}
	// The final word ends at the last byte and overlaps the one before it.
	unprintable := UnprintableBytes(Load64(s, n-8))
	for i := 0; i < n-8; i += 8 {
		unprintable |= UnprintableBytes(Load64(s, i))
	}
	return unprintable&HighBits == 0
}

func invalidNameBytes(w, ends uint64) uint64 {
	// Bit 7 of each byte carries the result; the other bits are noise that
	// the final mask discards. For a byte below 0x80, adding 0x80-k sets bit 7
	// exactly when the byte is at least k, and subtracting from 0x80+k sets it
	// exactly when the byte is at most k, and neither carries into the next
	// byte. A byte of 0x80 or more may carry, but the result holds its bit 7,
	// so the word fails whatever the carry does to the bytes above it.
	edges := (w + (0x80-'-')*EachByte) & ((0x80+'.')*EachByte - w)
	digits := (w + (0x80-'0')*EachByte) & ((0x80+'9')*EachByte - w)
	folded := w | 0x20*EachByte
	letters := (folded + (0x80-'a')*EachByte) & ((0x80+'z')*EachByte - folded)
	// '-' and '.' are adjacent codes, and of the two only '.' has bit 1 set.
	// A dot or hyphen beside a dot is an empty label or a hyphen on a label
	// edge, and so is one in the first or last byte of the name, which ends
	// marks as if a dot sat beside it.
	dots := edges & (w << 6)
	return ^(edges | digits | letters) | w | edges&(dots<<8|dots>>8|ends)
}

// UnprintableBytes returns a word that is nonzero under [HighBits] exactly
// when one of the eight bytes of w is not printable ASCII, 0x20 to 0x7E.
// Words may be combined with OR before the test.
func UnprintableBytes(w uint64) uint64 {
	// Bit 7 of a byte is set by the subtraction when the byte is below 0x20,
	// by the addition when it is 0x7F, and by w itself when it is above that.
	// A printable byte neither borrows nor carries, so the lowest unprintable
	// byte of a word always sets its bit 7. The bytes above it may be
	// misjudged, which is harmless: the result is only compared with zero.
	return (w - 0x20*EachByte) | w | (w + EachByte)
}

// Load64 returns s[i:i+8] as a little-endian word: byte i is the lowest.
// It panics if s holds fewer than i+8 bytes.
func Load64(s string, i int) uint64 {
	// The byte-wise little-endian form keeps results independent of the host
	// byte order. Slicing first leaves one bounds check, which lets the
	// compiler fuse the eight byte loads into one where the target allows.
	b := s[i : i+8]
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// Load32 returns s[i:i+4] as a little-endian value: byte i is the lowest.
// It panics if s holds fewer than i+4 bytes.
func Load32(s string, i int) uint64 {
	b := s[i : i+4]
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
}

func loadPadded(s string) uint64 {
	// The padding byte 'a' is printable and a name byte but neither a dot nor
	// a hyphen, so it changes no result for a string of at most one word.
	n := len(s)
	pad := uint64('a' * EachByte << (uint(n) * 8))
	if n >= 4 {
		// Two four-byte loads, which overlap below eight bytes, cover four to
		// eight bytes.
		return Load32(s, 0) | Load32(s, n-4)<<(uint(n-4)*8) | pad
	}
	if n == 0 {
		return pad
	}
	// The first, middle, and last bytes cover one to three bytes.
	return uint64(s[0]) | uint64(s[n>>1])<<(uint(n>>1)*8) | uint64(s[n-1])<<(uint(n-1)*8) | pad
}
