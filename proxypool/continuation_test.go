package proxypool_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func TestContinuationBlocksAcrossCyclesAndShards(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", ""} {
		content := "first\n" + strings.Repeat("x", 257) + "\r\n\n" + strings.Repeat("y", 128) + "\n" + strings.Repeat("z", 129) + ending
		path := writeFile(t, content)
		want := contentLines(content)
		for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
			for _, blockBytes := range []int{1, 7, 16, 64} {
				for _, seed := range []uint64{1, 3, 7} {
					for _, prefetch := range []bool{false, true} {
						options := proxypool.Options{
							Mode:                  mode,
							SequentialBufferBytes: blockBytes,
							BlockBytes:            blockBytes,
							RegionBytes:           int64(blockBytes * 3),
							MaxLineBytes:          257,
							Seed:                  seed,
							Prefetch:              prefetch,
							Reuse:                 true,
						}
						pool, err := proxypool.Open(path, options)
						if err != nil {
							t.Fatal(err)
						}
						for range 3 {
							got, err := collectMixed(pool, len(want), true)
							if err != nil || !slices.Equal(sortedCopy(got), sortedCopy(want)) {
								t.Fatalf("mode %d block %d seed %d prefetch %v: cycle = %q, %v; want %q", mode, blockBytes, seed, prefetch, got, err, want)
							}
						}
						if err := pool.Reset(); err != nil {
							t.Fatal(err)
						}
						got, err := collectLimited(pool, len(want))
						pool.Close()
						if err != nil || !slices.Equal(sortedCopy(got), sortedCopy(want)) {
							t.Fatalf("mode %d block %d seed %d prefetch %v: reset = %q, %v; want %q", mode, blockBytes, seed, prefetch, got, err, want)
						}
						if mode == proxypool.ModeSequential {
							continue
						}
						options.Reuse, options.ShardCount = false, 3
						got = nil
						for shard := range options.ShardCount {
							options.ShardIndex = shard
							pool, err := proxypool.Open(path, options)
							if err != nil {
								t.Fatal(err)
							}
							lines, err := collectMixed(pool, len(want)+1, true)
							pool.Close()
							if err != nil {
								t.Fatal(err)
							}
							got = append(got, lines...)
						}
						if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
							t.Fatalf("block %d seed %d prefetch %v: shards = %q, want %q", blockBytes, seed, prefetch, got, want)
						}
					}
				}
			}
		}
	}
}

func BenchmarkSequentialContinuation(b *testing.B) {
	path := writeFile(b, strings.Repeat(strings.Repeat("x", 1<<10)+"\n", 200))
	pool, err := proxypool.Open(path, proxypool.Options{
		Reuse:                 true,
		SequentialBufferBytes: 16,
		MaxLineBytes:          1 << 10,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	buffer := make([]byte, 0, 1<<10)
	if buffer, err = pool.NextBytes(buffer); err != nil {
		b.Fatal(err)
	}
	b.SetBytes((1 << 10) + 1)
	b.ReportAllocs()
	for b.Loop() {
		buffer, err = pool.NextBytes(buffer[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
}
