//go:build linux || darwin

package structuralindex_test

import (
	"os"
	"syscall"
	"testing"
)

func guardedPage(t *testing.T) []byte {
	t.Helper()
	size := os.Getpagesize()
	region, err := syscall.Mmap(-1, 0, 3*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("Mmap: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(region); err != nil {
			t.Errorf("Munmap: %v", err)
		}
	})
	for _, guard := range [][]byte{region[:size], region[2*size:]} {
		if err := syscall.Mprotect(guard, syscall.PROT_NONE); err != nil {
			t.Fatalf("Mprotect: %v", err)
		}
	}
	return region[size : 2*size : 2*size]
}
