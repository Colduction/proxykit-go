// Package fileopen_test verifies file opening and descriptor access through [fileopen].
package fileopen_test

import (
	"errors"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

// TestOpen checks [fileopen.Open] with sequential hints and missing files.
func TestOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("1.2.3.4:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sequential := range []bool{false, true} {
		file, err := fileopen.Open(path, os.O_RDONLY, 0, sequential)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		content, err := io.ReadAll(io.NewSectionReader(file, 0, 1<<10))
		file.Close()
		if err != nil || string(content) != "1.2.3.4:8080\n" {
			t.Fatalf("sequential %v: read %q, %v", sequential, content, err)
		}
	}
	if _, err := fileopen.Open(filepath.Join(t.TempDir(), "missing"), os.O_RDONLY, 0, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open of a missing file = %v", err)
	}
}

// TestWithFD checks [fileopen.WithFD] with nil files and callback errors.
func TestWithFD(t *testing.T) {
	if err := fileopen.WithFD(nil, func(uintptr) error { return nil }); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("WithFD(nil) = %v, want ErrInvalid", err)
	}
	file, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sentinel := errors.New("sentinel")
	if err := fileopen.WithFD(file, func(fd uintptr) error { return sentinel }); err != sentinel {
		t.Fatalf("WithFD = %v, want the callback's error", err)
	}
}

// TestUnavailableCalls checks unsupported [fileopen.Fadvise] and [fileopen.FcntlInt] calls.
func TestUnavailableCalls(t *testing.T) {
	if runtime.GOOS != "linux" || bits.UintSize == 32 {
		if err := fileopen.Fadvise(0, 0, 0, 0); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Fadvise = %v, want an unsupported-operation error", err)
		}
	}
	if runtime.GOOS != "darwin" {
		if _, err := fileopen.FcntlInt(0, 0, 0); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("FcntlInt = %v, want an unsupported-operation error", err)
		}
	}
}
