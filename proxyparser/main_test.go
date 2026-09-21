package proxyparser_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

// TestMain selects the structural index backend named by PROXYKIT_BACKEND,
// one of portable, avx2, avx512, and neon, so that the tests and benchmarks
// of this package can run against each backend the machine supports.
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
