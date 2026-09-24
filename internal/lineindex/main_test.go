package lineindex_test

import (
	"fmt"
	"os"
	"testing"
	"unsafe"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

// TestMain selects the vector backend named by PROXYKIT_BACKEND, one of
// portable, avx2, avx512, and neon, for the benchmarks of this package; the
// tests loop over every backend on their own.
func TestMain(m *testing.M) {
	if name := os.Getenv("PROXYKIT_BACKEND"); name != "" {
		selected := false
		for _, backend := range []structuralindex.Backend{structuralindex.Portable, structuralindex.AVX2, structuralindex.AVX512, structuralindex.NEON} {
			if backend.String() == name {
				structuralindex.SetBackend(backend)
				selected = structuralindex.ActiveBackend() == backend
			}
		}
		if !selected {
			fmt.Fprintf(os.Stderr, "PROXYKIT_BACKEND=%s is not supported on this machine\n", name)
			os.Exit(2)
		}
	}
	fmt.Fprintf(os.Stderr, "structuralindex backend: %v\n", structuralindex.ActiveBackend())
	os.Exit(m.Run())
}

func unsafeUint32s(b []byte) []uint32 {
	// It views b, whose length is a multiple of 4 and whose address is
	// 4-byte aligned, as uint32 elements, so that a test can place dst against a
	// protected page.
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
}
