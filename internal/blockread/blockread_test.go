package blockread_test

import (
	"bytes"
	"errors"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/colduction/proxykit-go/internal/blockread"
)

func writeFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pattern(n int) []byte {
	content := make([]byte, n)
	for i := range content {
		content[i] = byte(i*7 + i>>12)
	}
	return content
}

func TestMakeBuffer(t *testing.T) {
	for _, capacity := range []int{0, 1, 4095, blockread.AlignedBytes - 1, blockread.AlignedBytes, 1 << 20, 4<<20 + 4098} {
		length := min(3, capacity)
		buffer := blockread.MakeBuffer(length, capacity)
		if len(buffer) != length || cap(buffer) != capacity {
			t.Fatalf("capacity %d: len %d cap %d", capacity, len(buffer), cap(buffer))
		}
		if capacity >= blockread.AlignedBytes && !blockread.PageAligned(buffer[1:]) {
			t.Fatalf("capacity %d: index 1 does not start a page", capacity)
		}
		want := int64(capacity)
		if capacity >= blockread.AlignedBytes {
			want += blockread.PageBytes - 1
		}
		if got := blockread.BufferBytes(capacity); got != want {
			t.Fatalf("BufferBytes(%d) = %d, want %d", capacity, got, want)
		}
	}
}

func TestReaderReadAt(t *testing.T) {
	content := pattern(10_000)
	for _, sequential := range []bool{false, true} {
		file, err := blockread.OpenSource(writeFile(t, content), sequential)
		if err != nil {
			t.Fatalf("OpenSource: %v", err)
		}
		var reader blockread.Reader
		reader.Init(file)
		for _, test := range []struct {
			offset int64
			length int
			read   int
			err    error
		}{
			{0, 4096, 4096, nil},
			{1, 4096, 4096, nil},
			{9_000, 1_000, 1_000, nil},
			{9_000, 2_000, 1_000, io.EOF},
			{10_000, 10, 0, io.EOF},
			{12_000, 10, 0, io.EOF},
			{5, 0, 0, nil},
		} {
			p := make([]byte, test.length)
			read, err := reader.ReadAt(p, test.offset)
			if read != test.read || !errors.Is(err, test.err) && err != test.err {
				t.Fatalf("sequential %v: ReadAt(%d, %d) = %d, %v, want %d, %v", sequential, test.length, test.offset, read, err, test.read, test.err)
			}
			if !bytes.Equal(p[:read], content[min(test.offset, int64(len(content))):][:read]) {
				t.Fatalf("sequential %v: ReadAt(%d, %d) read the wrong bytes", sequential, test.length, test.offset)
			}
		}
		file.Close()
	}
}

func TestReaderReadAtBoundaries(t *testing.T) {
	path := writeFile(t, []byte("abcdef"))
	file, err := blockread.OpenSource(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reference, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	var reader blockread.Reader
	reader.Init(file)
	for _, offset := range []int64{0, 6, 7, -1, -2, -3} {
		for _, length := range []int{0, 1, 8} {
			got, want := make([]byte, length), make([]byte, length)
			n, err := reader.ReadAt(got, offset)
			wantN, wantErr := reference.ReadAt(want, offset)
			if n != wantN || (err == nil) != (wantErr == nil) || errors.Is(err, io.EOF) != errors.Is(wantErr, io.EOF) || !bytes.Equal(got, want) {
				t.Errorf("ReadAt(%d bytes, %d) = %d, %v, %q; os.File = %d, %v, %q", length, offset, n, err, got, wantN, wantErr, want)
			}
		}
	}
}

func TestHints(t *testing.T) {
	file, err := blockread.OpenSource(writeFile(t, pattern(1<<20)), false)
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	defer file.Close()
	var hints blockread.Hints
	supported := runtime.GOOS == "darwin" || runtime.GOOS == "linux" && bits.UintSize == 64
	if got := hints.Open(file); got != supported {
		t.Fatalf("Open = %v on %s/%s", got, runtime.GOOS, runtime.GOARCH)
	}
	if supported && !hints.Advise(4096, 512<<10) {
		t.Fatal("Advise was rejected")
	}
}
