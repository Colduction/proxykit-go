package structuralindex_test

import (
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

func isHostNameByteWise(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			b := label[i]
			if !(b|0x20 >= 'a' && b|0x20 <= 'z') && !(b >= '0' && b <= '9') && b != '-' {
				return false
			}
		}
	}
	return true
}

func isPrintableByteWise(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func checkHostName(t testing.TB, s string) {
	t.Helper()
	if got, want := structuralindex.IsHostName(s), isHostNameByteWise(s); got != want {
		t.Fatalf("IsHostName(%q) = %v, want %v", s, got, want)
	}
}

func checkPrintable(t testing.TB, s string) {
	t.Helper()
	if got, want := structuralindex.IsPrintable(s), isPrintableByteWise(s); got != want {
		t.Fatalf("IsPrintable(%q) = %v, want %v", s, got, want)
	}
}

func TestIsHostNameEveryByteAtEveryPosition(t *testing.T) {
	for n := 0; n <= 70; n++ {
		text := []byte(strings.Repeat("a", n))
		checkHostName(t, string(text))
		for position := range n {
			for value := range 256 {
				text[position] = byte(value)
				checkHostName(t, string(text))
			}
			text[position] = 'a'
		}
	}
}

// TestIsHostNameAdjacentEdges puts every pair of name bytes at every pair of
// adjacent positions, which covers pairs inside a word, across two words, and
// inside the overlapping final word.
func TestIsHostNameAdjacentEdges(t *testing.T) {
	const pairBytes = "a0-."
	for n := 2; n <= 40; n++ {
		text := []byte(strings.Repeat("a", n))
		for position := range n - 1 {
			for _, first := range []byte(pairBytes) {
				for _, second := range []byte(pairBytes) {
					text[position], text[position+1] = first, second
					checkHostName(t, string(text))
				}
			}
			text[position], text[position+1] = 'a', 'a'
		}
	}
}

func TestIsHostNameExhaustiveShortNames(t *testing.T) {
	const alphabet = "aZ0-._"
	var visit func(prefix []byte, remaining int)
	visit = func(prefix []byte, remaining int) {
		checkHostName(t, string(prefix))
		if remaining == 0 {
			return
		}
		for _, b := range []byte(alphabet) {
			visit(append(prefix, b), remaining-1)
		}
	}
	visit(nil, 7)
	for _, width := range []int{9, 17} {
		filler := strings.Repeat("a", width-7)
		visit = func(prefix []byte, remaining int) {
			if remaining == 0 {
				checkHostName(t, filler+string(prefix))
				checkHostName(t, string(prefix)+filler)
				return
			}
			for _, b := range []byte(alphabet[2:5]) {
				visit(append(prefix, b), remaining-1)
			}
		}
		visit(nil, 7)
	}
}

func TestIsPrintableEveryByteAtEveryPosition(t *testing.T) {
	for n := 0; n <= 40; n++ {
		text := []byte(strings.Repeat("~", n))
		checkPrintable(t, string(text))
		for position := range n {
			for value := range 256 {
				text[position] = byte(value)
				checkPrintable(t, string(text))
			}
			text[position] = ' '
		}
	}
}

func FuzzIsHostName(f *testing.F) {
	f.Add("proxy.example.com")
	f.Add("a-.b")
	f.Add("xn--bcher-kva.example")
	f.Fuzz(func(t *testing.T, s string) {
		checkHostName(t, s)
	})
}

func FuzzIsPrintable(f *testing.F) {
	f.Add("s3cr3t pass~")
	f.Add("tab\there")
	f.Fuzz(func(t *testing.T, s string) {
		checkPrintable(t, s)
	})
}
