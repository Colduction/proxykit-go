package lineindex_test

import (
	"bytes"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/colduction/proxykit-go/internal/lineindex"
	"github.com/colduction/proxykit-go/internal/structuralindex"
)

func backends(t testing.TB) []structuralindex.Backend {
	// It returns every backend this machine runs, Portable first, and
	// restores the active one when the test ends.
	t.Helper()
	initial := structuralindex.ActiveBackend()
	t.Cleanup(func() { structuralindex.SetBackend(initial) })
	var available []structuralindex.Backend
	for _, backend := range []structuralindex.Backend{structuralindex.Portable, structuralindex.AVX2, structuralindex.AVX512, structuralindex.NEON} {
		structuralindex.SetBackend(backend)
		if structuralindex.ActiveBackend() == backend {
			available = append(available, backend)
		}
	}
	structuralindex.SetBackend(initial)
	return available
}

func endsByteWise(src []byte, base uint32) []uint32 {
	// It is the oracle: the line ends of src, one byte at a time.
	var ends []uint32
	for i, b := range src {
		if b == '\n' {
			ends = append(ends, base+uint32(i)+1)
		}
	}
	return ends
}

func checkCarriageReturn(t testing.TB, backend structuralindex.Backend, examined []byte, cr bool) {
	// It checks the cr hint of Ends for the examined bytes: it
	// must be true when a line feed follows a carriage return, which puts the line
	// feed after src[0], and false when there is no carriage return.
	t.Helper()
	switch {
	case bytes.Contains(examined, []byte("\r\n")) && !cr:
		t.Fatalf("%v: Ends(%q) cr = false after a CRLF", backend, examined)
	case bytes.IndexByte(examined, '\r') < 0 && cr:
		t.Fatalf("%v: Ends(%q) cr = true without a carriage return", backend, examined)
	}
}

func checkEnds(t testing.TB, backend structuralindex.Backend, src []byte, base uint32, room int) {
	// It runs Ends on src with room elements of dst, checks the result
	// against the oracle and the room contract, and resumes with growing room
	// until src is consumed.
	t.Helper()
	want := endsByteWise(src, base)
	dst := make([]uint32, room)
	var (
		got      []uint32
		position int
	)
	for position < len(src) {
		// Poison the slots so that a missing store shows.
		for i := range dst {
			dst[i] = 0xdeadbeef
		}
		rest := src[position:]
		written, consumed, cr := lineindex.Ends(dst, rest, base+uint32(position))
		progress := len(dst) >= min(64, len(rest))
		switch {
		case consumed < 0 || consumed > len(rest) || written < 0 || written > len(dst):
			t.Fatalf("%v: Ends(room %d, %d bytes) = %d, %d out of range", backend, len(dst), len(rest), written, consumed)
		case progress && consumed == 0:
			t.Fatalf("%v: Ends(room %d, %d bytes) made no progress", backend, len(dst), len(rest))
		case consumed < len(rest) && (len(dst)-written >= 64 || len(dst)-written >= len(rest)-consumed):
			t.Fatalf("%v: Ends(room %d, %d bytes) stopped at %d with room for %d", backend, len(dst), len(rest), consumed, len(dst)-written)
		}
		examined := endsByteWise(rest[:consumed], base+uint32(position))
		if !slices.Equal(dst[:written], examined) {
			t.Fatalf("%v: Ends(room %d, %q) = %v, want %v", backend, len(dst), rest[:consumed], dst[:written], examined)
		}
		checkCarriageReturn(t, backend, rest[:consumed], cr)
		got = append(got, dst[:written]...)
		position += consumed
		if !progress || consumed == 0 {
			dst = make([]uint32, 2*len(dst)+1)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%v: Ends(%q) = %v, want %v", backend, src, got, want)
	}
}

func TestEndsEveryByteAtEveryPosition(t *testing.T) {
	values := []byte("\n\r\x00\x0b\x0c\x8a\x8d\xff a")
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		// The storage offset changes the alignment of src, and the
		// neighbours hold line feeds that must not leak into the result.
		storage := bytes.Repeat([]byte("ab\ncd\r\nef"), 40)
		for n := 0; n <= 200; n++ {
			sweep := values
			offsets := []int{n % 64}
			if n <= 72 && !testing.Short() {
				sweep = make([]byte, 256)
				for i := range sweep {
					sweep[i] = byte(i)
				}
				offsets = []int{0, 1, 7}
			}
			for _, offset := range offsets {
				src := storage[offset : offset+n : offset+n]
				for position := range n {
					saved := src[position]
					for _, value := range sweep {
						src[position] = value
						checkEnds(t, backend, src, 0, 64)
					}
					src[position] = saved
				}
				checkEnds(t, backend, src, 0, 64)
			}
		}
	}
}

func TestEndsLineFeedDensities(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4))
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for blocks := 1; blocks <= 3; blocks++ {
			for _, tail := range []int{0, 1, 31, 63} {
				for count := 0; count <= 64; count++ {
					src := bytes.Repeat([]byte{'x'}, blocks*64+tail)
					for block := range blocks {
						// First, last, and random placements of count line
						// feeds within the block.
						switch count % 3 {
						case 0:
							for i := range count {
								src[block*64+i] = '\n'
							}
						case 1:
							for i := range count {
								src[block*64+63-i] = '\n'
							}
						default:
							for _, i := range random.Perm(64)[:count] {
								src[block*64+i] = '\n'
							}
						}
					}
					checkEnds(t, backend, src, 0, 64)
					checkEnds(t, backend, src, 0, len(src)+64)
				}
			}
		}
	}
}

func TestEndsRoomContract(t *testing.T) {
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for _, every := range []int{1, 2, 3, 7, 47, 200} {
			src := make([]byte, 200)
			for i := range src {
				src[i] = 'y'
				if i%every == every-1 {
					src[i] = '\n'
				}
			}
			for n := 0; n <= len(src); n++ {
				for room := 0; room <= 70; room++ {
					checkEnds(t, backend, src[:n:n], 17, room)
				}
			}
		}
	}
}

func TestEndsCarriageReturnEveryPosition(t *testing.T) {
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for n := 1; n <= 192; n++ {
			for position := range n {
				src := bytes.Repeat([]byte{'z'}, n)
				src[position] = '\r'
				if position+1 < n {
					src[position+1] = '\n'
				}
				checkEnds(t, backend, src, 0, 64)
			}
		}
	}
}

func TestEndsBase(t *testing.T) {
	src := bytes.Repeat([]byte("line\n"), 60)
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for _, base := range []uint32{0, 1, 4096, math.MaxUint32 - uint32(len(src))} {
			checkEnds(t, backend, src, base, 64)
		}
	}
}

// TestEndsBesideProtectedPages places src against both edges of a page whose
// neighbours fault on access, and dst against the end of such a page, so a
// kernel that reads or writes outside them crashes the test.
func TestEndsBesideProtectedPages(t *testing.T) {
	page := guardedPage(t)
	dstPage := guardedPage(t)
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for n := 0; n <= 200; n++ {
			for _, start := range []int{0, 1, 63, len(page) - n, len(page) - n - 1} {
				if start < 0 || start+n > len(page) {
					continue
				}
				src := page[start : start+n : start+n]
				for i := range src {
					src[i] = "ab\r\n"[(i+start)%4]
				}
				want := endsByteWise(src, 0)
				for _, room := range []int{64, n, len(want)} {
					if room < min(64, n) {
						continue
					}
					words := unsafeUint32s(dstPage[len(dstPage)-4*room:])
					written, consumed, cr := lineindex.Ends(words, src, 0)
					examined := endsByteWise(src[:consumed], 0)
					if !slices.Equal(words[:written], examined) {
						t.Fatalf("%v: Ends(%d bytes at %d, room %d) = %v, want %v", backend, n, start, room, words[:written], examined)
					}
					checkCarriageReturn(t, backend, src[:consumed], cr)
					if consumed == n && !slices.Equal(examined, want) {
						t.Fatalf("%v: incomplete result", backend)
					}
				}
			}
		}
	}
}

func TestMaxGap(t *testing.T) {
	random := rand.New(rand.NewPCG(5, 6))
	for _, backend := range backends(t) {
		structuralindex.SetBackend(backend)
		for n := 0; n <= 100; n++ {
			for _, top := range []uint32{1, 100, math.MaxUint32 / 128} {
				offsets := make([]uint32, n)
				var value uint32
				for i := range offsets {
					value += random.Uint32N(top)
					offsets[i] = value
				}
				var want uint32
				for i := 1; i < n; i++ {
					want = max(want, offsets[i]-offsets[i-1])
				}
				if got := lineindex.MaxGap(offsets); got != want {
					t.Fatalf("%v: MaxGap(%v) = %d, want %d", backend, offsets, got, want)
				}
			}
		}
		// A gap in every lane position, and gaps near the uint32 limit.
		for n := 2; n <= 40; n++ {
			for at := 1; at < n; at++ {
				offsets := make([]uint32, n)
				for i := range offsets {
					offsets[i] = uint32(i)
					if i >= at {
						offsets[i] += math.MaxUint32 - uint32(n)
					}
				}
				if got, want := lineindex.MaxGap(offsets), math.MaxUint32-uint32(n)+1; got != want {
					t.Fatalf("%v: MaxGap with the gap at %d of %d = %d, want %d", backend, at, n, got, want)
				}
			}
		}
	}
}

func FuzzEnds(f *testing.F) {
	f.Add([]byte("http://user:pass@proxy.example:8080\nsocks5://a:b@c:1\r\n"), uint8(64), uint32(0))
	f.Add(bytes.Repeat([]byte("\n"), 130), uint8(3), uint32(7))
	available := backends(f)
	f.Fuzz(func(t *testing.T, src []byte, room uint8, base uint32) {
		if uint64(base)+uint64(len(src)) > math.MaxUint32 {
			return
		}
		for _, backend := range available {
			structuralindex.SetBackend(backend)
			checkEnds(t, backend, src, base, int(room))
		}
	})
}

func FuzzMaxGap(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	available := backends(f)
	f.Fuzz(func(t *testing.T, steps []byte) {
		offsets := make([]uint32, len(steps))
		var value, want uint32
		for i, step := range steps {
			value += uint32(step) * uint32(step)
			offsets[i] = value
			if i > 0 {
				want = max(want, offsets[i]-offsets[i-1])
			}
		}
		for _, backend := range available {
			structuralindex.SetBackend(backend)
			if got := lineindex.MaxGap(offsets); got != want {
				t.Fatalf("%v: MaxGap(%v) = %d, want %d", backend, offsets, got, want)
			}
		}
	})
}
