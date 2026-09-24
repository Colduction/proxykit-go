package proxypool_test

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

// BenchmarkLargeFile reads a generated file of PROXYKIT_BENCH_LARGE_GIB GiB
// from start to end, through NextBytes and through NextBatch, and reports the
// throughput. It is skipped unless the variable is set; the file comes from
// the page cache after it is written, so the figures are the pool's throughput
// over a warm cache, which bounds its throughput over any storage.
func BenchmarkLargeFile(b *testing.B) {
	gib, _ := strconv.Atoi(os.Getenv("PROXYKIT_BENCH_LARGE_GIB"))
	if gib <= 0 {
		b.Skip("set PROXYKIT_BENCH_LARGE_GIB to the file size in GiB")
	}
	path, size := largeFile(b, int64(gib)<<30)
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential", options: proxypool.Options{}},
		{name: "shuffled", options: proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 1}},
	} {
		b.Run(test.name+"/nextbytes", func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			buffer := make([]byte, 0, 128)
			for b.Loop() {
				pool, err := proxypool.Open(path, test.options)
				if err != nil {
					b.Fatal(err)
				}
				for {
					buffer, err = pool.NextBytes(buffer[:0])
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
					sink += int(buffer[0])
				}
				pool.Close()
			}
		})
		b.Run(test.name+"/batch", func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			var batch proxypool.Batch
			for b.Loop() {
				pool, err := proxypool.Open(path, test.options)
				if err != nil {
					b.Fatal(err)
				}
				for {
					err := pool.NextBatch(&batch)
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
					sink += drainLines(&batch)
				}
				pool.Close()
			}
		})
	}
}

func largeFile(b *testing.B, size int64) (string, int64) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "large.txt")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	var (
		written int64
		line    []byte
	)
	for i := 0; written < size; i++ {
		line = append(line[:0], "http://user"...)
		line = strconv.AppendInt(line, int64(i), 10)
		line = append(line, ":pass"...)
		line = strconv.AppendInt(line, int64(i), 10)
		line = append(line, "@proxy"...)
		line = strconv.AppendInt(line, int64(i%100_000), 10)
		line = append(line, ".example:"...)
		line = strconv.AppendInt(line, int64(8000+i%50_000), 10)
		line = append(line, '\n')
		n, err := writer.Write(line)
		if err != nil {
			b.Fatal(err)
		}
		written += int64(n)
	}
	if err := writer.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	return path, written
}
