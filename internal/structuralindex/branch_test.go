package structuralindex_test

import (
	"testing"
	"unsafe"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

// TestBuildCoversBothLoadDirections runs every byte value at every position
// of texts placed against the end of a page followed by a protected page, and
// asserts that the addresses select the forward and the backward load of both
// the one-vector and the two-vector path of the AVX2 kernel.
func TestBuildCoversBothLoadDirections(t *testing.T) {
	page := guardedPage(t)
	for _, backend := range vectorBackends(t) {
		// Only the AVX2 kernel chooses a load by address, and
		// TestBuildEveryByteAtEveryPosition owns the sweep of all byte values.
		if backend != structuralindex.AVX2 {
			continue
		}
		structuralindex.SetBackend(backend)
		var loads [2][2]int
		for n := 1; n <= structuralindex.MaxLen; n++ {
			for _, start := range []int{0, len(page) - 64, len(page) - 63, len(page) - 32, len(page) - 31, len(page) - n} {
				if start+n > len(page) {
					continue
				}
				text := page[start : start+n]
				for i := range page {
					page[i] = ":@/.-5q\x7f"[i%8]
				}
				// The kernel loads one vector of 32 bytes for a text of at
				// most 32 bytes and two otherwise, and it loads backward when
				// a forward load would cross the end of the page.
				vectors, direction := 0, 0
				if n > 32 {
					vectors = 1
				}
				if int(uintptr(unsafe.Pointer(unsafe.SliceData(text)))&4095) > 4096-32*(vectors+1) {
					direction = 1
				}
				loads[vectors][direction]++
				for position := range n {
					saved := text[position]
					for _, value := range []byte(":@/.-09AZaz\x00\x1f \x7e\x7f\x80\xff") {
						text[position] = value
						checkBuild(t, backend, unsafeString(text))
					}
					text[position] = saved
				}
			}
		}
		for vectors, directions := range loads {
			if directions[0] == 0 || directions[1] == 0 {
				t.Errorf("%v: %d-vector path took %d forward and %d backward loads, want both", backend, vectors+1, directions[0], directions[1])
			}
		}
	}
}
