package proxypool_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

// TestOpenFileInvalidModePreservesSource checks that iteration-mode validation precedes truncation.
func TestOpenFileInvalidModePreservesSource(t *testing.T) {
	const content = "source must survive\n"
	path := writeFile(t, content)
	file, err := proxypool.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0, proxypool.Mode(255))
	if file != nil {
		file.Close()
	}
	if err == nil || file != nil {
		t.Errorf("OpenFile with invalid mode = %v, %v; want nil file and an error", file, err)
	}
	if got := readFile(t, path); got != content {
		t.Fatalf("source after rejected open = %q, want %q", got, content)
	}
}

// TestOpenFileInvalidModeDoesNotCreateSource checks that iteration-mode validation precedes file creation.
func TestOpenFileInvalidModeDoesNotCreateSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.txt")
	file, err := proxypool.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600, proxypool.Mode(255))
	if file != nil {
		file.Close()
	}
	if err == nil || file != nil {
		t.Errorf("OpenFile with invalid mode = %v, %v; want nil file and an error", file, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat after rejected open = %v, want nonexistent source", err)
	}
}
