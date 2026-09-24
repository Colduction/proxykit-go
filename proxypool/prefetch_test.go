package proxypool_test

import (
	"errors"
	"io"
	"math/bits"
	"runtime"
	"slices"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func prefetchSupported() bool {
	// It reports whether Options.Prefetch takes effect here: the
	// hints exist on Linux and macOS, fadvise needs a 64-bit Linux, and
	// Windows reads ahead with overlapped reads.
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "linux" && bits.UintSize == 64
}

func TestPrefetchMatchesWithout(t *testing.T) {
	path, _ := makeProxyFile(t, 3_000)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, blockBytes := range []int{64, 4 << 10, 1 << 20} {
			options := proxypool.Options{
				Mode:                  mode,
				SequentialBufferBytes: blockBytes,
				BlockBytes:            blockBytes,
				RegionBytes:           int64(blockBytes) * 4,
				MaxLineBytes:          128,
				Seed:                  21,
				Reuse:                 true,
			}
			plain, err := proxypool.Open(path, options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			want, err := collectLimited(plain, 7_000)
			plain.Close()
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			options.Prefetch = true
			prefetching, err := proxypool.Open(path, options)
			if err != nil {
				t.Fatalf("Open with Prefetch: %v", err)
			}
			got, err := collectLimited(prefetching, 7_000)
			stats := prefetching.Stats()
			prefetching.Close()
			if err != nil {
				t.Fatalf("collect with Prefetch: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("mode %d block %d: Prefetch changed the lines", mode, blockBytes)
			}
			if stats.Prefetch != prefetchSupported() {
				t.Fatalf("mode %d block %d: Stats.Prefetch = %v on %s/%s", mode, blockBytes, stats.Prefetch, runtime.GOOS, runtime.GOARCH)
			}
			if stats.RetainedBytes > stats.MaxRetainedBytes {
				t.Fatalf("retained %d exceeds %d", stats.RetainedBytes, stats.MaxRetainedBytes)
			}
		}
	}
}

func TestPrefetchStartsNoGoroutines(t *testing.T) {
	path, _ := makeProxyFile(t, 2_000)
	before := runtime.NumGoroutine()
	pool, err := proxypool.Open(path, proxypool.Options{SequentialBufferBytes: 4 << 10, Prefetch: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for range 500 {
		if _, err := pool.Next(); err != nil {
			t.Fatalf("Next: %v", err)
		}
	}
	during := runtime.NumGoroutine()
	pool.Close()
	if during != before {
		t.Fatalf("goroutines = %d while reading, %d before", during, before)
	}
}

func TestPrefetchZeroAllocation(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation adds allocations to the read path")
	}
	path, _ := makeProxyFile(t, 400)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(path, proxypool.Options{
			Mode:                  mode,
			Reuse:                 true,
			SequentialBufferBytes: 4 << 10,
			BlockBytes:            4 << 10,
			RegionBytes:           8 << 10,
			MaxLineBytes:          128,
			Seed:                  1,
			Prefetch:              true,
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var batch proxypool.Batch
		drain := func() {
			if err := pool.NextBatch(&batch); err != nil {
				panic(err)
			}
			for range batch.Lines() {
			}
		}
		for range 8 {
			drain()
		}
		allocations := testing.AllocsPerRun(100, drain)
		pool.Close()
		if allocations != 0 {
			t.Fatalf("mode %d: allocations = %.2f, want 0", mode, allocations)
		}
	}
}

func collectMixed(pool *proxypool.Pool, limit int, mixed bool) ([]string, error) {
	// It returns up to limit lines through NextBatch, and through Next after
	// every third batch when mixed, so that both paths load blocks.
	var (
		lines []string
		batch proxypool.Batch
	)
	for count := 0; len(lines) < limit; count++ {
		if mixed && count%3 == 2 {
			line, err := pool.Next()
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			if err != nil {
				return lines, err
			}
			lines = append(lines, line)
			continue
		}
		if err := pool.NextBatch(&batch); err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
		for line := range batch.Lines() {
			lines = append(lines, string(line))
		}
	}
	return lines, nil
}

func TestPrefetchNextBatchMatchesWithout(t *testing.T) {
	path, _ := makeProxyFile(t, 3_000)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, blockBytes := range []int{64, 4 << 10, 1 << 20} {
			for _, mixed := range []bool{false, true} {
				options := proxypool.Options{
					Mode:                  mode,
					SequentialBufferBytes: blockBytes,
					BlockBytes:            blockBytes,
					RegionBytes:           int64(blockBytes) * 4,
					MaxLineBytes:          128,
					Seed:                  5,
					Reuse:                 true,
				}
				plain, err := proxypool.Open(path, options)
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				want, err := collectMixed(plain, 7_000, mixed)
				plain.Close()
				if err != nil {
					t.Fatalf("collect: %v", err)
				}
				options.Prefetch = true
				prefetching, err := proxypool.Open(path, options)
				if err != nil {
					t.Fatalf("Open with Prefetch: %v", err)
				}
				got, err := collectMixed(prefetching, 7_000, mixed)
				prefetching.Close()
				if err != nil {
					t.Fatalf("collect with Prefetch: %v", err)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("mode %d block %d mixed %v: Prefetch changed the lines", mode, blockBytes, mixed)
				}
			}
		}
	}
}

// A pool with Prefetch may have a read in flight after every NextBatch.
// Reset, Close, and dropping the pool must each settle it.
func TestPrefetchReadInFlight(t *testing.T) {
	path, _ := makeProxyFile(t, 3_000)
	options := proxypool.Options{Mode: proxypool.ModeShuffled, BlockBytes: 4 << 10, RegionBytes: 16 << 10, MaxLineBytes: 128, Seed: 3, Prefetch: true}
	reference, err := proxypool.Open(path, proxypool.Options{Mode: options.Mode, BlockBytes: options.BlockBytes, RegionBytes: options.RegionBytes, MaxLineBytes: options.MaxLineBytes, Seed: options.Seed})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want, err := collect(reference)
	reference.Close()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var batch proxypool.Batch
	pool, err := proxypool.Open(path, options)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := pool.NextBatch(&batch); err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if err := pool.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err := collectMixed(pool, len(want)+1, true)
	if err != nil {
		t.Fatalf("collect after Reset: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatal("lines after Reset differ from a fresh pool")
	}
	if err := pool.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := pool.NextBatch(&batch); err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close with a read in flight: %v", err)
	}
	if err := pool.NextBatch(&batch); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("NextBatch after Close = %v, want ErrClosed", err)
	}
	for range 4 {
		dropped, err := proxypool.Open(path, options)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := dropped.NextBatch(&batch); err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
	}
	runtime.GC()
	runtime.GC()
}
