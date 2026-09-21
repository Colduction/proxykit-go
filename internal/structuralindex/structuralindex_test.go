package structuralindex_test

import (
	"math/bits"
	"strings"
	"testing"
	"unsafe"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

var classes = []struct {
	name    string
	class   structuralindex.Class
	matches func(b byte) bool
}{
	{"Colon", structuralindex.Colon, func(b byte) bool { return b == ':' }},
	{"At", structuralindex.At, func(b byte) bool { return b == '@' }},
	{"Dot", structuralindex.Dot, func(b byte) bool { return b == '.' }},
	{"Hyphen", structuralindex.Hyphen, func(b byte) bool { return b == '-' }},
	{"Alnum", structuralindex.Alnum, func(b byte) bool { return b >= '0' && b <= '9' || b|0x20 >= 'a' && b|0x20 <= 'z' }},
	{"Unprintable", structuralindex.Unprintable, func(b byte) bool { return b < 0x20 || b > 0x7e }},
}

func vectorBackends(t testing.TB) []structuralindex.Backend {
	// SetBackend ignores a backend without a kernel on this machine, so
	// probing each one yields the ones that run here.
	t.Helper()
	initial := structuralindex.ActiveBackend()
	t.Cleanup(func() { structuralindex.SetBackend(initial) })
	var available []structuralindex.Backend
	for _, backend := range []structuralindex.Backend{structuralindex.AVX2, structuralindex.AVX512, structuralindex.NEON} {
		structuralindex.SetBackend(backend)
		if structuralindex.ActiveBackend() == backend {
			available = append(available, backend)
		}
	}
	structuralindex.SetBackend(initial)
	return available
}

func unsafeString(text []byte) string {
	// The alias makes a kernel read text at its own address.
	return unsafe.String(unsafe.SliceData(text), len(text))
}

func checkBuild(t testing.TB, backend structuralindex.Backend, text string) {
	t.Helper()
	var ix structuralindex.Index
	if !ix.Build(text) {
		t.Fatalf("%v: Build(%q) = false", backend, text)
	}
	for _, c := range classes {
		var want uint64
		for i := range len(text) {
			if c.matches(text[i]) {
				want |= 1 << uint(i)
			}
		}
		if got := ix.Bitmap(c.class); got != want {
			t.Fatalf("%v: Build(%q).Bitmap(%s) = %#x, want %#x", backend, text, c.name, got, want)
		}
	}
}

func TestBuildEveryByteAtEveryPosition(t *testing.T) {
	for _, backend := range vectorBackends(t) {
		structuralindex.SetBackend(backend)
		// The storage offset changes the alignment of the text, and the
		// neighbours hold class bytes that must not leak into a bitmap.
		storage := []byte(strings.Repeat(":@/.-5q\x7f", 16))
		for n := 1; n <= structuralindex.MaxLen; n++ {
			for offset := range 4 {
				text := storage[offset : offset+n]
				for position := range n {
					saved := text[position]
					for value := range 256 {
						text[position] = byte(value)
						checkBuild(t, backend, unsafeString(text))
					}
					text[position] = saved
				}
			}
		}
	}
}

func TestBuildRejectsUnindexableText(t *testing.T) {
	backends := append(vectorBackends(t), structuralindex.Portable)
	for _, backend := range backends {
		structuralindex.SetBackend(backend)
		tests := []struct {
			text string
			want bool
		}{
			{"", false},
			{"a", backend != structuralindex.Portable},
			{strings.Repeat("a", structuralindex.MaxLen), backend != structuralindex.Portable},
			{strings.Repeat("a", structuralindex.MaxLen+1), false},
		}
		for _, test := range tests {
			var ix structuralindex.Index
			if got := ix.Build(test.text); got != test.want {
				t.Errorf("%v: Build(%d bytes) = %v, want %v", backend, len(test.text), got, test.want)
			}
		}
	}
}

// TestBuildBesideProtectedPages places text against both edges of a page
// whose neighbours fault on access, so a kernel that reads outside the pages
// of the text crashes the test.
func TestBuildBesideProtectedPages(t *testing.T) {
	page := guardedPage(t)
	for _, backend := range vectorBackends(t) {
		structuralindex.SetBackend(backend)
		for n := 1; n <= structuralindex.MaxLen; n++ {
			for shift := range 80 {
				for _, start := range []int{shift, len(page) - n - shift} {
					if start < 0 || start+n > len(page) {
						continue
					}
					text := page[start : start+n]
					for i := range text {
						text[i] = ":@/.-5q\x7f"[(i+shift)%8]
					}
					checkBuild(t, backend, unsafeString(text))
				}
			}
		}
	}
}

func TestMasks(t *testing.T) {
	for n := 0; n <= structuralindex.MaxLen; n++ {
		if got := bits.OnesCount64(structuralindex.LowMask(n)); got != n {
			t.Errorf("LowMask(%d) has %d bits", n, got)
		}
		if n < structuralindex.MaxLen && structuralindex.LowMask(n)>>uint(n) != 0 {
			t.Errorf("LowMask(%d) = %#x sets a bit at or above %d", n, structuralindex.LowMask(n), n)
		}
	}
	tests := []struct {
		start, end int
		want       uint64
	}{
		{0, 0, 0},
		{0, 64, ^uint64(0)},
		{64, 64, 0},
		{3, 3, 0},
		{3, 7, 0b0111_1000},
		{63, 64, 1 << 63},
	}
	for _, test := range tests {
		if got := structuralindex.Range(test.start, test.end); got != test.want {
			t.Errorf("Range(%d, %d) = %#x, want %#x", test.start, test.end, got, test.want)
		}
	}
}

func TestNext(t *testing.T) {
	backends := vectorBackends(t)
	if len(backends) == 0 {
		t.Skip("no vector backend")
	}
	text := strings.Repeat("a", 20) + ":" + strings.Repeat("b", 42) + ":"
	var ix structuralindex.Index
	if !ix.Build(text) {
		t.Fatal("Build = false")
	}
	tests := []struct{ from, want int }{
		{0, 20}, {20, 20}, {21, 63}, {63, 63}, {64, -1},
	}
	for _, test := range tests {
		if got := ix.Next(structuralindex.Colon, test.from); got != test.want {
			t.Errorf("Next(Colon, %d) = %d, want %d", test.from, got, test.want)
		}
	}
	if got := ix.Next(structuralindex.At, 0); got != -1 {
		t.Errorf("Next(At, 0) = %d, want -1", got)
	}
}

func TestClassOf(t *testing.T) {
	for value := range 256 {
		b := byte(value)
		class, ok := structuralindex.ClassOf(b)
		var want int
		for _, c := range classes[:4] {
			if c.matches(b) {
				want++
				if !ok || class != c.class {
					t.Errorf("ClassOf(%q) = %v, %v, want %s", b, class, ok, c.name)
				}
			}
		}
		if ok != (want == 1) {
			t.Errorf("ClassOf(%q) ok = %v", b, ok)
		}
	}
}

func FuzzBuild(f *testing.F) {
	f.Add("http://proxy.example.com:8080")
	f.Add("socks5://alice:s3cr3t@203.0.113.27:1080")
	f.Add("\x00\x7f\x80\xff@Z[`az{")
	backends := vectorBackends(f)
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) == 0 || len(text) > structuralindex.MaxLen {
			return
		}
		for _, backend := range backends {
			structuralindex.SetBackend(backend)
			checkBuild(t, backend, text)
		}
	})
}
