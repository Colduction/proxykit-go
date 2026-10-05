package blockread_test

import (
	"math"
	"math/bits"
	"testing"

	"github.com/colduction/proxykit-go/internal/blockread"
	"github.com/colduction/proxykit-go/internal/fileopen"
)

// TestHintsRangeBoundaries checks [blockread.Hints.Advise] with invalid ranges
// and final chunks near [math.MaxInt64].
func TestHintsRangeBoundaries(t *testing.T) {
	if bits.UintSize == 32 {
		t.Skip("file hints are unavailable on 32-bit Linux")
	}
	file, err := blockread.OpenSource(writeFile(t, []byte("data")), false)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var hints blockread.Hints
	if !hints.Open(file) {
		t.Fatal("Open rejected a regular file")
	}
	for _, test := range []struct {
		offset int64
		length int
	}{
		{-1, 1},
		{math.MinInt64, 1},
		{0, -1},
		{math.MaxInt64, 1},
		{math.MaxInt64 - 127, 128},
	} {
		if hints.Advise(test.offset, test.length) {
			t.Errorf("Advise(%d, %d) accepted an invalid range", test.offset, test.length)
		}
	}
	if !hints.Advise(math.MaxInt64, 0) {
		t.Error("Advise rejected an empty range")
	}
	t.Run("FinalChunk", func(t *testing.T) {
		const posixFadvWillNeed = 3
		if err := fileopen.WithFD(file, func(fd uintptr) error {
			return fileopen.Fadvise(int(fd), math.MaxInt64-1, 1, posixFadvWillNeed)
		}); err != nil {
			t.Skipf("file system rejects hints near MaxInt64: %v", err)
		}
		for _, length := range []int{1, 128 << 10, 128<<10 + 1} {
			offset := int64(math.MaxInt64) - int64(length)
			if !hints.Advise(offset, length) {
				t.Errorf("Advise(%d, %d) rejected a valid range", offset, length)
			}
		}
	})
}
