package proxypool_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

// TestSmallSourceRetainedMemory checks that continuation buffers stay bounded by the source size.
func TestSmallSourceRetainedMemory(t *testing.T) {
	content := strings.Repeat("x", 100) + "\n"
	path := writeFile(t, content)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, blockBytes := range []int{8, 16, 64} {
			pool, err := proxypool.Open(path, proxypool.Options{
				Mode:                  mode,
				SequentialBufferBytes: blockBytes,
				BlockBytes:            blockBytes,
				RegionBytes:           int64(blockBytes) * 3,
				MaxLineBytes:          1 << 30,
				Seed:                  1,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := collect(pool)
			stats := pool.Stats()
			pool.Close()
			if err != nil || !slices.Equal(got, []string{content[:len(content)-1]}) {
				t.Fatalf("mode %d block %d: lines = %q, %v", mode, blockBytes, got, err)
			}
			maximum := int64(len(content)+3) + int64(blockBytes+1)*4
			if stats.RetainedBytes > maximum {
				t.Fatalf("mode %d block %d: retained %d bytes for a %d-byte source; maximum %d", mode, blockBytes, stats.RetainedBytes, len(content), maximum)
			}
		}
	}
}

// TestBatchStorageFromLargerSourceIsDropped checks bounded storage reuse across files with identical options.
func TestBatchStorageFromLargerSourceIsDropped(t *testing.T) {
	for _, prefetch := range []bool{false, true} {
		options := proxypool.Options{SequentialBufferBytes: 64 << 10, MaxLineBytes: 1 << 20, Reuse: true, Prefetch: prefetch}
		large, err := proxypool.Open(writeFile(t, strings.Repeat("x\n", 64<<10)), options)
		if err != nil {
			t.Fatal(err)
		}
		defer large.Close()
		var batch proxypool.Batch
		if err := large.NextBatch(&batch); err != nil {
			t.Fatal(err)
		}
		const content = "small\n"
		small, err := proxypool.Open(writeFile(t, content), options)
		if err != nil {
			t.Fatal(err)
		}
		defer small.Close()
		for range 3 {
			if err := small.NextBatch(&batch); err != nil {
				t.Fatal(err)
			}
			if line, ok := batch.Next(); !ok || string(line) != "small" {
				t.Fatalf("prefetch %v: Next = %q, %v", prefetch, line, ok)
			}
			if stats := small.Stats(); stats.RetainedBytes > int64(len(content)+3+(len(content)+1)*4) {
				t.Fatalf("prefetch %v: retained %d bytes after adopting storage for a %d-byte source", prefetch, stats.RetainedBytes, len(content))
			}
		}
	}
}

// BenchmarkSmallSourceContinuation measures allocations and retained storage for a fresh small-file pool.
func BenchmarkSmallSourceContinuation(b *testing.B) {
	path := writeFile(b, strings.Repeat("x", 100)+"\n")
	options := proxypool.Options{SequentialBufferBytes: 16, MaxLineBytes: 1 << 30}
	b.ReportAllocs()
	for b.Loop() {
		pool, err := proxypool.Open(path, options)
		if err != nil {
			b.Fatal(err)
		}
		_, err = pool.NextBytes(nil)
		stats := pool.Stats()
		pool.Close()
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(stats.RetainedBytes), "retained-B")
	}
}
