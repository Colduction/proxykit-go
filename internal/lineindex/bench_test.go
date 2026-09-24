package lineindex_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/internal/lineindex"
	"github.com/colduction/proxykit-go/internal/structuralindex"
)

var shapes = []struct {
	name string
	line string
}{
	{"16B", "1.2.3.4:8080abc\n"},
	{"47B", "http://user1:pass1@proxy1.example.com:8080abcd\n"},
	{"92B", "http://customer-alice-cc-us-sessid-8f3a91c2d7:Zx9kQ2mP7vL4@gate.residential.example.net:777\n"},
	{"crlf47", "http://user1:pass1@proxy1.example.com:8080abc\r\n"},
	{"empty", "\n"},
	{"none", strings.Repeat("x", 64)},
}

const sourceBytes = 64 << 10

func source(line string) []byte {
	return bytes.Repeat([]byte(line), sourceBytes/len(line))
}

func BenchmarkEnds(b *testing.B) {
	// Each shape is a 64 KiB source of proxy lines of one length, or one of
	// the extremes: nothing but line feeds, or none.
	for _, backend := range backends(b) {
		for _, shape := range shapes {
			b.Run(fmt.Sprintf("%v/%s", backend, shape.name), func(b *testing.B) {
				structuralindex.SetBackend(backend)
				src := source(shape.line)
				dst := make([]uint32, len(src)+64)
				lines := bytes.Count(src, []byte{'\n'})
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				for b.Loop() {
					if written, consumed, _ := lineindex.Ends(dst, src, 0); written != lines || consumed != len(src) {
						b.Fatalf("Ends = %d, %d", written, consumed)
					}
				}
				reportPerLine(b, lines)
			})
		}
	}
	// The reference is the loop that proxypool ran before, one IndexByte
	// call per line, which the portable tier must beat.
	for _, shape := range shapes {
		b.Run("indexbyte/"+shape.name, func(b *testing.B) {
			src := source(shape.line)
			dst := make([]uint32, 0, len(src)+64)
			lines := bytes.Count(src, []byte{'\n'})
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				dst = dst[:0]
				for search := 0; search < len(src); {
					newline := bytes.IndexByte(src[search:], '\n')
					if newline < 0 {
						break
					}
					search += newline + 1
					dst = append(dst, uint32(search))
				}
			}
			reportPerLine(b, lines)
		})
	}
}

func BenchmarkMaxGap(b *testing.B) {
	for _, backend := range backends(b) {
		for _, n := range []int{64, 1 << 10, 64 << 10} {
			b.Run(fmt.Sprintf("%v/%d", backend, n), func(b *testing.B) {
				structuralindex.SetBackend(backend)
				offsets := make([]uint32, n)
				for i := range offsets {
					offsets[i] = uint32(i * 47)
				}
				b.ReportAllocs()
				for b.Loop() {
					if lineindex.MaxGap(offsets) != 47 {
						b.Fatal("MaxGap")
					}
				}
				reportPerLine(b, n)
			})
		}
	}
}

func reportPerLine(b *testing.B, lines int) {
	if lines > 0 {
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(lines), "ns/line")
	}
}
