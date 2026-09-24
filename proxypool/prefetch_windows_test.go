package proxypool_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/colduction/proxykit-go/internal/blockread"
	"github.com/colduction/proxykit-go/proxypool"
)

// Reads without buffering cover whole pages from each block's first byte,
// and the byte before a block that does not follow the last one loaded comes
// from a separate read. Small regions mix both kinds of block, and the file
// ends inside a page.
func TestPrefetchDirectMatchesWithout(t *testing.T) {
	defer blockread.SetForceDirect(blockread.SetForceDirect(true))
	path, _ := makeProxyFile(t, 30_000)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, blockBytes := range []int{64 << 10, 128 << 10} {
			for _, mixed := range []bool{false, true} {
				options := proxypool.Options{
					Mode:                  mode,
					SequentialBufferBytes: blockBytes,
					BlockBytes:            blockBytes,
					RegionBytes:           int64(blockBytes) * 3,
					Seed:                  11,
					Reuse:                 true,
				}
				plain, err := proxypool.Open(path, options)
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				want, err := collectMixed(plain, 70_000, mixed)
				plain.Close()
				if err != nil {
					t.Fatalf("collect: %v", err)
				}
				options.Prefetch = true
				direct, err := proxypool.Open(path, options)
				if err != nil {
					t.Fatalf("Open with Prefetch: %v", err)
				}
				if !proxypool.Direct(direct) {
					t.Fatalf("mode %d block %d: pool does not read without buffering", mode, blockBytes)
				}
				got, err := collectMixed(direct, 70_000, mixed)
				stats := direct.Stats()
				direct.Close()
				if err != nil {
					t.Fatalf("collect with Prefetch: %v", err)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("mode %d block %d mixed %v: reads without buffering changed the lines", mode, blockBytes, mixed)
				}
				if stats.RetainedBytes > stats.MaxRetainedBytes {
					t.Fatalf("retained %d exceeds %d", stats.RetainedBytes, stats.MaxRetainedBytes)
				}
			}
		}
	}
}

// A source that shrinks while reads ahead are in flight makes them fail at
// once or come back short; the pool must report the change rather than wait
// for a read that never started, in both kinds of read ahead.
func TestPrefetchSourceTruncated(t *testing.T) {
	for _, direct := range []bool{false, true} {
		func() {
			defer blockread.SetForceDirect(blockread.SetForceDirect(direct))
			path, _ := makeProxyFile(t, 30_000)
			pool, err := proxypool.Open(path, proxypool.Options{SequentialBufferBytes: 64 << 10, Prefetch: true})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer pool.Close()
			var batch proxypool.Batch
			if err := pool.NextBatch(&batch); err != nil {
				t.Fatalf("NextBatch: %v", err)
			}
			if err := os.Truncate(path, 70<<10); err != nil {
				t.Fatalf("Truncate: %v", err)
			}
			for range 64 {
				err = pool.NextBatch(&batch)
				if err != nil {
					break
				}
			}
			if !errors.Is(err, proxypool.ErrSourceChanged) {
				t.Fatalf("direct %v: NextBatch after truncation = %v, want ErrSourceChanged", direct, err)
			}
		}()
	}
}
