// Package proxypool_test verifies proxy-file iteration, storage bounds, and pool lifecycles.
package proxypool_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

// TestMain selects the vector backend named by the PROXYKIT_BACKEND environment variable and runs tests and benchmarks.
// Supported values are portable, avx2, avx512, and neon when available on the current processor.
func TestMain(m *testing.M) {
	if name := os.Getenv("PROXYKIT_BACKEND"); name != "" {
		selected := false
		for _, backend := range availableBackends() {
			if backend.String() == name {
				structuralindex.SetBackend(backend)
				selected = true
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

func availableBackends() []structuralindex.Backend {
	previous := structuralindex.ActiveBackend()
	defer structuralindex.SetBackend(previous)
	var backends []structuralindex.Backend
	for _, backend := range []structuralindex.Backend{structuralindex.Portable, structuralindex.AVX2, structuralindex.AVX512, structuralindex.NEON} {
		structuralindex.SetBackend(backend)
		if structuralindex.ActiveBackend() == backend {
			backends = append(backends, backend)
		}
	}
	return backends
}
