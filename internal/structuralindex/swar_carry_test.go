package structuralindex_test

import (
	"strings"
	"testing"
)

// The word tests let a byte that fails carry or borrow into the byte above
// it. These tests put every pair and triple of boundary bytes at every
// position, so that a carry which hid a failing byte would show.

var carryBytes = []byte{
	0x00, 0x1f, 0x20, 0x2c, '-', '.', '/', '0', '9', ':', '@', 'A', 'Z', '[', '`', 'a', 'z', '{',
	0x7e, 0x7f, 0x80, 0x81, 0xad, 0xae, 0xaf, 0xb9, 0xba, 0xd2, 0xd3, 0xe0, 0xfa, 0xfb, 0xfe, 0xff,
}

func TestIsHostNameByteGroupsAtEveryPosition(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 10, 15, 16, 17, 22, 23, 24, 31, 63} {
		text := []byte(strings.Repeat("a", n))
		for position := range n {
			for _, first := range carryBytes {
				text[position] = first
				if position+1 < n {
					for _, second := range carryBytes {
						text[position+1] = second
						checkHostName(t, string(text))
					}
					text[position+1] = 'a'
				}
				checkHostName(t, string(text))
			}
			text[position] = 'a'
		}
	}
	for _, n := range []int{3, 8, 9, 17} {
		text := []byte(strings.Repeat("a", n))
		for position := 0; position+2 < n; position++ {
			for _, first := range carryBytes {
				for _, second := range carryBytes {
					for _, third := range carryBytes {
						text[position], text[position+1], text[position+2] = first, second, third
						checkHostName(t, string(text))
					}
				}
			}
			text[position], text[position+1], text[position+2] = 'a', 'a', 'a'
		}
	}
}

func TestIsPrintableByteGroupsAtEveryPosition(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 10, 15, 16, 17, 24, 25, 70} {
		text := []byte(strings.Repeat(" ", n))
		for position := range n {
			for _, first := range carryBytes {
				text[position] = first
				if position+1 < n {
					for _, second := range carryBytes {
						text[position+1] = second
						checkPrintable(t, string(text))
					}
					text[position+1] = '~'
				}
				checkPrintable(t, string(text))
			}
			text[position] = ' '
		}
	}
	for _, n := range []int{3, 8, 9, 17} {
		text := []byte(strings.Repeat("~", n))
		for position := 0; position+2 < n; position++ {
			for _, first := range carryBytes {
				for _, second := range carryBytes {
					for _, third := range carryBytes {
						text[position], text[position+1], text[position+2] = first, second, third
						checkPrintable(t, string(text))
					}
				}
			}
			text[position], text[position+1], text[position+2] = '~', '~', '~'
		}
	}
}
