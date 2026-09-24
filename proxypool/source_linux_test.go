package proxypool_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/colduction/proxykit-go/proxypool"
)

func TestOpenRejectsFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		pool, err := proxypool.Open(path, proxypool.Options{})
		if pool != nil {
			pool.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Open accepted a FIFO")
		}
	case <-time.After(2 * time.Second):
		// A writer releases an open blocked on the FIFO before the test fails.
		// Read-write and nonblocking flags make this cleanup independent of
		// whether the reader has reached the kernel yet.
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatalf("Open blocked on a FIFO, and releasing it failed: %v", err)
		}
		defer syscall.Close(fd)
		select {
		case <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("Open remained blocked after a FIFO writer became available")
		}
		t.Fatal("Open blocked waiting for a FIFO writer")
	}
}
