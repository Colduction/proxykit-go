package proxypool_test

import (
	"slices"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func FuzzPoolMatchesFile(f *testing.F) {
	for i, layout := range batchLayouts {
		f.Add([]byte(layout), uint8(i), uint8(i), uint64(i+1))
	}
	f.Fuzz(func(t *testing.T, content []byte, blockBytes, selection uint8, seed uint64) {
		if len(content) > 8192 {
			t.Skip("bounded file-layout fuzz input")
		}
		path := writeFile(t, string(content))
		options := proxypool.Options{
			Mode:                  proxypool.Mode(selection & 1),
			SequentialBufferBytes: int(blockBytes) + 1,
			BlockBytes:            int(blockBytes) + 1,
			RegionBytes:           3 * (int64(blockBytes) + 1),
			MaxLineBytes:          len(content) + 1,
			Seed:                  seed | 1,
			ShardCount:            1,
			Prefetch:              selection&8 != 0,
		}
		if options.Mode == proxypool.ModeShuffled {
			options.ShardCount += int(selection>>1) % 4
		}
		var got []string
		for shard := range options.ShardCount {
			options.ShardIndex = shard
			pool, err := proxypool.Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Close() })
			lines, err := collectMixed(pool, len(content)+1, true)
			if err != nil {
				t.Fatal(err)
			}
			stats := pool.Stats()
			if stats.Cursor != int64(len(lines)) || stats.RetainedBytes > stats.MaxRetainedBytes {
				t.Fatalf("shard %d: cursor %d, lines %d, retained %d, maximum %d", shard, stats.Cursor, len(lines), stats.RetainedBytes, stats.MaxRetainedBytes)
			}
			if err := pool.Reset(); err != nil {
				t.Fatal(err)
			}
			replay, err := collectMixed(pool, len(content)+1, false)
			if err != nil || !slices.Equal(lines, replay) {
				t.Fatalf("shard %d: replay = %q, %v; want %q", shard, replay, err, lines)
			}
			got = append(got, lines...)
		}
		want := contentLines(string(content))
		if options.Mode == proxypool.ModeShuffled {
			slices.Sort(got)
			slices.Sort(want)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("lines = %q, want %q", got, want)
		}
	})
}
