package lineindex

import (
	"bytes"
	"encoding/binary"
	"math/bits"
)

const (
	eachByte        = 0x0101010101010101
	lowSeven        = 0x7f * eachByte
	highBits        = 0x80 * eachByte
	newlines        = '\n' * eachByte
	gather          = 0x0002040810204081
	searchLineBytes = 44
)

func newlineBits(w uint64) uint64 {
	x := w ^ newlines
	zero := ^((x&lowSeven + lowSeven) | x) & highBits
	return zero * gather >> 56
}

func endsScalar(dst []uint32, src []byte, base uint32) (written, consumed int, cr bool) {
	var previous int
	search := false
	for consumed < len(src) {
		if search {
			for written < len(dst) {
				i := bytes.IndexByte(src[consumed:], '\n')
				if i < 0 {
					return written, len(src), cr
				}
				end := consumed + i
				cr = cr || src[max(end-1, 0)] == '\r'
				dst[written] = base + uint32(end) + 1
				written++
				search = end-previous >= searchLineBytes
				consumed, previous = end+1, end+1
				if !search {
					break
				}
			}
			if search {
				break
			}
			continue
		}
		if len(src)-consumed < blockBytes {
			search = true
			continue
		}
		if len(dst)-written < blockBytes {
			break
		}
		block := src[consumed : consumed+blockBytes : consumed+blockBytes]
		mask := newlineBits(binary.LittleEndian.Uint64(block[0:])) |
			newlineBits(binary.LittleEndian.Uint64(block[8:]))<<8 |
			newlineBits(binary.LittleEndian.Uint64(block[16:]))<<16 |
			newlineBits(binary.LittleEndian.Uint64(block[24:]))<<24 |
			newlineBits(binary.LittleEndian.Uint64(block[32:]))<<32 |
			newlineBits(binary.LittleEndian.Uint64(block[40:]))<<40 |
			newlineBits(binary.LittleEndian.Uint64(block[48:]))<<48 |
			newlineBits(binary.LittleEndian.Uint64(block[56:]))<<56
		count := bits.OnesCount64(mask)
		if mask != 0 {
			previous = consumed + blockBytes - bits.LeadingZeros64(mask)
		}
		ends := dst[written : written+2 : written+2]
		position := bits.TrailingZeros64(mask)
		ends[0] = base + uint32(consumed+position) + 1
		cr = cr || src[max(consumed+position-1, 0)] == '\r'
		mask &= mask - 1
		position = bits.TrailingZeros64(mask)
		ends[1] = base + uint32(consumed+position) + 1
		cr = cr || src[max(consumed+position-1, 0)] == '\r'
		mask &= mask - 1
		for i := written + 2; mask != 0; i++ {
			position = bits.TrailingZeros64(mask)
			dst[i] = base + uint32(consumed+position) + 1
			cr = cr || src[consumed+position-1] == '\r'
			mask &= mask - 1
		}
		written += count
		consumed += blockBytes
		search = count < 2
	}
	return written, consumed, cr
}

func maxGapWords(offsets []uint32) (largest uint32, gaps int) {
	gaps = (len(offsets) - 1) &^ 3
	var m0, m1, m2, m3 uint32
	for i := 0; i < gaps; i += 4 {
		o := offsets[i : i+5 : i+5]
		m0 = max(m0, o[1]-o[0])
		m1 = max(m1, o[2]-o[1])
		m2 = max(m2, o[3]-o[2])
		m3 = max(m3, o[4]-o[3])
	}
	return max(m0, m1, m2, m3), gaps
}
