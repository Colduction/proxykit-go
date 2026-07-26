package proxypool_test

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func writeFile(t testing.TB, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func makeProxyFile(t testing.TB, lines int) (string, []string) {
	t.Helper()
	var content strings.Builder
	want := make([]string, lines)
	for i := range lines {
		want[i] = fmt.Sprintf("http://user%d:pass%d@proxy%d.example:%d", i, i, i, 8000+i%65535)
		content.WriteString(want[i])
		content.WriteByte('\n')
	}
	return writeFile(t, content.String()), want
}

func contentLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(content, "\n")+1)
	for len(content) > 0 {
		newline := strings.IndexByte(content, '\n')
		if newline < 0 {
			return append(lines, content)
		}
		line := strings.TrimSuffix(content[:newline], "\r")
		lines = append(lines, line)
		content = content[newline+1:]
	}
	return lines
}

func collect(pool *proxypool.Pool) ([]string, error) {
	var lines []string
	for {
		line, err := pool.Next()
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}
}

func sortedCopy(values []string) []string {
	copyOfValues := slices.Clone(values)
	sort.Strings(copyOfValues)
	return copyOfValues
}

func TestOpenValidation(t *testing.T) {
	path := writeFile(t, "proxy\n")
	tests := []struct {
		name    string
		path    string
		options proxypool.Options
	}{
		{name: "invalid mode", path: path, options: proxypool.Options{Mode: proxypool.Mode(99)}},
		{name: "missing", path: path + ".missing"},
		{name: "negative shard", path: path, options: proxypool.Options{Mode: proxypool.ModeShuffled, ShardIndex: -1}},
		{name: "shard outside count", path: path, options: proxypool.Options{Mode: proxypool.ModeShuffled, ShardCount: 2, ShardIndex: 2}},
		{name: "sequential shard", path: path, options: proxypool.Options{ShardCount: 2}},
		{name: "sharded reuse", path: path, options: proxypool.Options{Mode: proxypool.ModeShuffled, Reuse: true, ShardCount: 2}},
		{name: "region below block", path: path, options: proxypool.Options{Mode: proxypool.ModeShuffled, BlockBytes: 1024, RegionBytes: 512}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := proxypool.Open(test.path, test.options)
			if err == nil {
				pool.Close()
				t.Fatal("Open succeeded")
			}
		})
	}
}

func TestSequentialLineShapes(t *testing.T) {
	content := "\nalpha\r\nbeta\ngamma\r\nlast\r"
	pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
		SequentialBufferBytes: 3,
		MaxLineBytes:          32,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	got, err := collect(pool)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if want := contentLines(content); !slices.Equal(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
	if _, err = pool.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after exhaustion = %v, want io.EOF", err)
	}
}

func TestSequentialLongLineAndLimit(t *testing.T) {
	path := writeFile(t, strings.Repeat("x", 257)+"\r\ny\n")
	pool, err := proxypool.Open(path, proxypool.Options{
		SequentialBufferBytes: 7,
		MaxLineBytes:          257,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	line, err := pool.Next()
	if err != nil || line != strings.Repeat("x", 257) {
		t.Fatalf("Next = %d bytes, %v", len(line), err)
	}
	pool.Close()

	pool, err = proxypool.Open(path, proxypool.Options{
		SequentialBufferBytes: 7,
		MaxLineBytes:          256,
	})
	if err != nil {
		t.Fatalf("Open limited: %v", err)
	}
	defer pool.Close()
	if _, err = pool.Next(); !errors.Is(err, proxypool.ErrLineTooLong) {
		t.Fatalf("Next = %v, want ErrLineTooLong", err)
	}
	if _, err = pool.Next(); !errors.Is(err, proxypool.ErrLineTooLong) {
		t.Fatalf("terminal Next = %v, want ErrLineTooLong", err)
	}
}

func TestSequentialReuseAndReset(t *testing.T) {
	pool, err := proxypool.Open(writeFile(t, "a\nb\n"), proxypool.Options{Reuse: true, Seed: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	for i, want := range []string{"a", "b", "a", "b", "a"} {
		got, err := pool.Next()
		if err != nil || got != want {
			t.Fatalf("Next %d = %q, %v; want %q", i, got, err, want)
		}
	}
	if got := pool.Stats().Cycle; got != 2 {
		t.Fatalf("Cycle = %d, want 2", got)
	}
	if err = pool.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got, err := pool.Next(); err != nil || got != "a" {
		t.Fatalf("Next after Reset = %q, %v", got, err)
	}
}

func TestExhaustionPersistsUntilReset(t *testing.T) {
	path := writeFile(t, "proxy\n")
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, Seed: 1})
		if err != nil {
			t.Fatalf("Open %d: %v", mode, err)
		}
		if line, nextErr := pool.Next(); nextErr != nil || line != "proxy" {
			t.Fatalf("mode %d first Next = %q, %v", mode, line, nextErr)
		}
		for range 2 {
			if _, nextErr := pool.Next(); !errors.Is(nextErr, io.EOF) {
				t.Fatalf("mode %d exhausted Next = %v, want io.EOF", mode, nextErr)
			}
		}
		if err = pool.Reset(); err != nil {
			t.Fatalf("mode %d Reset: %v", mode, err)
		}
		if line, nextErr := pool.Next(); nextErr != nil || line != "proxy" {
			t.Fatalf("mode %d Next after Reset = %q, %v", mode, line, nextErr)
		}
		pool.Close()
	}
}

func TestEmptyFileReuseDoesNotLoop(t *testing.T) {
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(writeFile(t, ""), proxypool.Options{Mode: mode, Reuse: true})
		if err != nil {
			t.Fatalf("Open %d: %v", mode, err)
		}
		if _, err = pool.Next(); !errors.Is(err, io.EOF) {
			t.Errorf("mode %d: Next = %v, want io.EOF", mode, err)
		}
		pool.Close()
	}
}

func TestShuffledLineLayoutsEveryBoundary(t *testing.T) {
	contents := []string{
		"",
		"\n",
		"\n\n",
		"a",
		"a\n",
		"a\nb",
		"a\r\nb\n\nc\r",
		"0123456789abcdef\nshort\nlast",
		strings.Repeat("z", 31) + "\r\nend\n",
	}
	for _, content := range contents {
		for blockBytes := 1; blockBytes <= 17; blockBytes++ {
			name := fmt.Sprintf("length=%d/block=%d", len(content), blockBytes)
			t.Run(name, func(t *testing.T) {
				pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
					Mode:         proxypool.ModeShuffled,
					BlockBytes:   blockBytes,
					RegionBytes:  int64(blockBytes * 3),
					MaxLineBytes: 64,
					Seed:         0x12345678,
				})
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				defer pool.Close()
				got, err := collect(pool)
				if err != nil {
					t.Fatalf("collect: %v", err)
				}
				want := contentLines(content)
				if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
					t.Fatalf("lines = %#v, want %#v", got, want)
				}
			})
		}
	}
}

func TestShuffledLongLineCrossesBlocks(t *testing.T) {
	content := "first\n" + strings.Repeat("x", 63) + "\r\nlast"
	pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   8,
		RegionBytes:  24,
		MaxLineBytes: 63,
		Seed:         99,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	got, err := collect(pool)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if want := contentLines(content); !slices.Equal(sortedCopy(got), sortedCopy(want)) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestShuffledContinuationEndsWithSplitCRLFAtLimit(t *testing.T) {
	content := "aaaaaa\n" + strings.Repeat("x", 8) + "\r\nend"
	pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   8,
		RegionBytes:  8,
		MaxLineBytes: 8,
		Seed:         1,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	got, err := collect(pool)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if want := contentLines(content); !slices.Equal(sortedCopy(got), sortedCopy(want)) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestShuffledRandomizedLayouts(t *testing.T) {
	random := rand.New(rand.NewPCG(0x1234, 0x5678))
	for testIndex := range 250 {
		lineCount := random.IntN(40)
		var content strings.Builder
		for lineIndex := range lineCount {
			for range random.IntN(128) {
				content.WriteByte(byte('a' + random.IntN(26)))
			}
			if lineIndex < lineCount-1 || random.IntN(3) != 0 {
				if random.IntN(2) == 0 {
					content.WriteByte('\r')
				}
				content.WriteByte('\n')
			}
		}
		blockBytes := random.IntN(64) + 1
		pool, err := proxypool.Open(writeFile(t, content.String()), proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   blockBytes,
			RegionBytes:  int64(blockBytes * (random.IntN(5) + 1)),
			MaxLineBytes: 128,
			Seed:         random.Uint64() | 1,
		})
		if err != nil {
			t.Fatalf("case %d Open: %v", testIndex, err)
		}
		got, err := collect(pool)
		pool.Close()
		if err != nil {
			t.Fatalf("case %d collect: %v", testIndex, err)
		}
		want := contentLines(content.String())
		if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
			t.Fatalf("case %d: lines = %#v, want %#v", testIndex, got, want)
		}
	}
}

func TestShuffledLineTooLong(t *testing.T) {
	pool, err := proxypool.Open(writeFile(t, strings.Repeat("x", 65)), proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   8,
		RegionBytes:  24,
		MaxLineBytes: 64,
		Seed:         7,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if _, err = collect(pool); !errors.Is(err, proxypool.ErrLineTooLong) {
		t.Fatalf("collect = %v, want ErrLineTooLong", err)
	}
}

func TestShuffledResetReplaysOrder(t *testing.T) {
	path, _ := makeProxyFile(t, 100)
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   128,
		RegionBytes:  512,
		MaxLineBytes: 128,
		Seed:         42,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	first, err := collect(pool)
	if err != nil {
		t.Fatalf("first collect: %v", err)
	}
	if err = pool.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	second, err := collect(pool)
	if err != nil {
		t.Fatalf("second collect: %v", err)
	}
	if !slices.Equal(first, second) {
		t.Fatal("Reset did not replay deterministic order")
	}
}

func TestShuffledReuseStartsCompleteCycles(t *testing.T) {
	path, want := makeProxyFile(t, 73)
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		Reuse:        true,
		BlockBytes:   128,
		RegionBytes:  512,
		MaxLineBytes: 128,
		Seed:         101,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	for cycle := range 2 {
		got := make([]string, len(want))
		for i := range got {
			got[i], err = pool.Next()
			if err != nil {
				t.Fatalf("cycle %d, line %d: %v", cycle, i, err)
			}
		}
		if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
			t.Fatalf("cycle %d incomplete", cycle)
		}
	}
	if got := pool.Stats().Cycle; got != 1 {
		t.Fatalf("Cycle = %d, want 1", got)
	}
}

func TestShardsCollectivelyExactOnce(t *testing.T) {
	const shardCount = 7
	path, want := makeProxyFile(t, 1_000)
	var all []string
	for shardIndex := range shardCount {
		pool, err := proxypool.Open(path, proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   128,
			RegionBytes:  384,
			MaxLineBytes: 128,
			Seed:         987654321,
			ShardCount:   shardCount,
			ShardIndex:   shardIndex,
		})
		if err != nil {
			t.Fatalf("Open shard %d: %v", shardIndex, err)
		}
		got, err := collect(pool)
		pool.Close()
		if err != nil {
			t.Fatalf("collect shard %d: %v", shardIndex, err)
		}
		all = append(all, got...)
	}
	if !slices.Equal(sortedCopy(all), sortedCopy(want)) {
		t.Fatalf("shards returned %d lines, want %d exact lines", len(all), len(want))
	}
}

func TestShardsLongLineAcrossRegions(t *testing.T) {
	content := strings.Repeat("x", 100) + "\nlast\n"
	path := writeFile(t, content)
	want := contentLines(content)
	var all []string
	for shardIndex := range 4 {
		pool, err := proxypool.Open(path, proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   8,
			RegionBytes:  16,
			MaxLineBytes: 100,
			Seed:         123,
			ShardCount:   4,
			ShardIndex:   shardIndex,
		})
		if err != nil {
			t.Fatalf("Open shard %d: %v", shardIndex, err)
		}
		got, err := collect(pool)
		pool.Close()
		if err != nil {
			t.Fatalf("collect shard %d: %v", shardIndex, err)
		}
		all = append(all, got...)
	}
	if !slices.Equal(sortedCopy(all), sortedCopy(want)) {
		t.Fatalf("lines = %#v, want %#v", all, want)
	}
}

func TestShardWithoutAssignedRegionIsEmpty(t *testing.T) {
	pool, err := proxypool.Open(writeFile(t, "a\n"), proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   16,
		RegionBytes:  16,
		MaxLineBytes: 16,
		Seed:         1,
		ShardCount:   8,
		ShardIndex:   7,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if _, err = pool.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next = %v, want io.EOF", err)
	}
}

func TestConcurrentNextExactOnce(t *testing.T) {
	const workers = 8
	path, want := makeProxyFile(t, 5_000)
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   4 << 10,
		RegionBytes:  32 << 10,
		MaxLineBytes: 128,
		Seed:         55,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	lines := make(chan string, len(want))
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			for {
				line, err := pool.Next()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					errorsFound <- err
					return
				}
				lines <- line
			}
		})
	}
	wait.Wait()
	close(lines)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("Next: %v", err)
	}
	got := make([]string, 0, len(lines))
	for line := range lines {
		got = append(got, line)
	}
	if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
		t.Fatalf("got %d lines, want %d exact lines", len(got), len(want))
	}
}

func TestSourceChangeBecomesTerminal(t *testing.T) {
	path := writeFile(t, "a\nb\nc\nd\ne\nf\n")
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   4,
		RegionBytes:  4,
		MaxLineBytes: 8,
		Seed:         8,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if _, err = pool.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err = file.WriteString("changed\n"); err != nil {
		file.Close()
		t.Fatalf("append: %v", err)
	}
	file.Close()
	for range 10 {
		_, err = pool.Next()
		if errors.Is(err, proxypool.ErrSourceChanged) {
			break
		}
	}
	if !errors.Is(err, proxypool.ErrSourceChanged) {
		t.Fatalf("Next = %v, want ErrSourceChanged", err)
	}
	if _, err = pool.Next(); !errors.Is(err, proxypool.ErrSourceChanged) {
		t.Fatalf("terminal Next = %v, want ErrSourceChanged", err)
	}
}

func TestSequentialDoesNotReadAppendedData(t *testing.T) {
	path := writeFile(t, "a\n")
	pool, err := proxypool.Open(path, proxypool.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if line, err := pool.Next(); err != nil || line != "a" {
		t.Fatalf("first Next = %q, %v", line, err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err = file.WriteString("b\n"); err != nil {
		file.Close()
		t.Fatalf("append: %v", err)
	}
	file.Close()
	if _, err = pool.Next(); !errors.Is(err, proxypool.ErrSourceChanged) {
		t.Fatalf("Next = %v, want ErrSourceChanged", err)
	}
}

func TestTenTiBSparseFileOpensWithoutProportionalMemory(t *testing.T) {
	const tenTiB int64 = 10 << 40
	path := filepath.Join(t.TempDir(), "ten-tib.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err = file.Truncate(tenTiB); err != nil {
		file.Close()
		t.Skipf("filesystem cannot create 10 TiB sparse file: %v", err)
	}
	if err = file.Close(); err != nil {
		t.Fatalf("Close file: %v", err)
	}
	pool, err := proxypool.Open(path, proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	stats := pool.Stats()
	pool.Close()
	if stats.FileSize != tenTiB {
		t.Fatalf("FileSize = %d, want %d", stats.FileSize, tenTiB)
	}
	if stats.Blocks != tenTiB/proxypool.DefaultBlockBytes {
		t.Fatalf("Blocks = %d", stats.Blocks)
	}
	if stats.RetainedBytes != 0 {
		t.Fatalf("RetainedBytes at Open = %d, want 0", stats.RetainedBytes)
	}
	if stats.MaxRetainedBytes > 22<<20 {
		t.Fatalf("MaxRetainedBytes = %d, want bounded near 20 MiB", stats.MaxRetainedBytes)
	}
	if _, err = os.Stat(path + ".idx"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected sidecar: %v", err)
	}
}

func TestStatsMemoryBound(t *testing.T) {
	pool, err := proxypool.Open(writeFile(t, strings.Repeat("\n", 4096)), proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		BlockBytes:   4096,
		RegionBytes:  4096,
		MaxLineBytes: 16,
		Seed:         3,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if _, err = pool.NextBytes(make([]byte, 0, 16)); err != nil {
		t.Fatalf("NextBytes: %v", err)
	}
	stats := pool.Stats()
	if stats.RetainedBytes > stats.MaxRetainedBytes {
		t.Fatalf("retained %d exceeds maximum %d", stats.RetainedBytes, stats.MaxRetainedBytes)
	}
	if stats.Cursor != 1 || stats.Blocks != 1 || stats.Regions != 1 {
		t.Fatalf("Stats = %+v", stats)
	}
	if want := int64(4096 + (4096+1)*4); stats.RetainedBytes != want {
		t.Fatalf("RetainedBytes = %d, want %d", stats.RetainedBytes, want)
	}
}

func TestLargeLineLimitAllocatesLazily(t *testing.T) {
	const largeLimit = 1 << 30
	content := strings.Repeat("x", 100) + "\n"
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential", options: proxypool.Options{
			SequentialBufferBytes: 16,
			MaxLineBytes:          largeLimit,
		}},
		{name: "shuffled", options: proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   8,
			RegionBytes:  24,
			MaxLineBytes: largeLimit,
			Seed:         1,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, err := proxypool.Open(writeFile(t, content), test.options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer pool.Close()
			if _, err = pool.Next(); err != nil {
				t.Fatalf("Next: %v", err)
			}
			if retained := pool.Stats().RetainedBytes; retained > 1<<20 {
				t.Fatalf("RetainedBytes = %d, MaxLineBytes was allocated eagerly", retained)
			}
		})
	}
}

func TestNextBytesZeroAllocation(t *testing.T) {
	path, _ := makeProxyFile(t, 100)
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential", options: proxypool.Options{Reuse: true}},
		{name: "shuffled", options: proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			Reuse:        true,
			BlockBytes:   8 << 10,
			RegionBytes:  8 << 10,
			MaxLineBytes: 128,
			Seed:         1,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, err := proxypool.Open(path, test.options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer pool.Close()
			buffer := make([]byte, 0, 128)
			for range 200 {
				buffer, err = pool.NextBytes(buffer[:0])
				if err != nil {
					t.Fatalf("warmup: %v", err)
				}
			}
			allocations := testing.AllocsPerRun(1_000, func() {
				buffer, err = pool.NextBytes(buffer[:0])
				if err != nil {
					panic(err)
				}
			})
			if allocations != 0 {
				t.Fatalf("allocations = %.2f, want 0", allocations)
			}
		})
	}
}

func TestShuffledContinuationZeroAllocation(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation adds allocations to the ReadAt path")
	}
	path := writeFile(t, strings.Repeat(strings.Repeat("x", 1<<10)+"\n", 200))
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		Reuse:        true,
		BlockBytes:   16,
		RegionBytes:  1 << 20,
		MaxLineBytes: 1 << 10,
		Seed:         1,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	buffer := make([]byte, 0, 1<<10)
	for range 4 {
		buffer, err = pool.NextBytes(buffer[:0])
		if err != nil {
			t.Fatalf("warmup: %v", err)
		}
	}
	allocations := testing.AllocsPerRun(100, func() {
		buffer, err = pool.NextBytes(buffer[:0])
		if err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("allocations = %.2f, want 0", allocations)
	}
}

func TestCloseAndNilPool(t *testing.T) {
	pool, err := proxypool.New(writeFile(t, "a\n"), proxypool.ModeSequential, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err = pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err = pool.Next(); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("Next after Close = %v", err)
	}
	if err = pool.Reset(); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("Reset after Close = %v", err)
	}
	var nilPool *proxypool.Pool
	if _, err = nilPool.Next(); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("nil Next = %v", err)
	}
	if err = nilPool.Close(); err != nil {
		t.Fatalf("nil Close = %v", err)
	}
	zeroPool := new(proxypool.Pool)
	if _, err = zeroPool.Next(); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("zero Next = %v", err)
	}
	if err = zeroPool.Reset(); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("zero Reset = %v", err)
	}
	if err = zeroPool.Close(); err != nil {
		t.Fatalf("zero Close = %v", err)
	}
	if !zeroPool.Stats().Closed {
		t.Fatal("zero Stats did not report closed")
	}
}

const benchmarkLines = 100_000

func BenchmarkOpen(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		b.Run(fmt.Sprintf("mode=%d", mode), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, Seed: 1})
				if err != nil {
					b.Fatal(err)
				}
				pool.Close()
			}
		})
	}
}

func BenchmarkNextBytes(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential", options: proxypool.Options{Reuse: true}},
		{name: "shuffled", options: proxypool.Options{Mode: proxypool.ModeShuffled, Reuse: true, Seed: 1}},
	} {
		b.Run(test.name, func(b *testing.B) {
			pool, err := proxypool.Open(path, test.options)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()
			buffer := make([]byte, 0, 128)
			buffer, err = pool.NextBytes(buffer)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				buffer, err = pool.NextBytes(buffer[:0])
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkShuffledContinuation(b *testing.B) {
	path := writeFile(b, strings.Repeat(strings.Repeat("x", 1<<10)+"\n", 200))
	pool, err := proxypool.Open(path, proxypool.Options{
		Mode:         proxypool.ModeShuffled,
		Reuse:        true,
		BlockBytes:   16,
		RegionBytes:  1 << 20,
		MaxLineBytes: 1 << 10,
		Seed:         1,
	})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	buffer := make([]byte, 0, 1<<10)
	if buffer, err = pool.NextBytes(buffer); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	b.SetBytes((1 << 10) + 1)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buffer, err = pool.NextBytes(buffer[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExhaustedNextBytes(b *testing.B) {
	path := writeFile(b, "proxy\n")
	for _, test := range []struct {
		name string
		mode proxypool.Mode
	}{
		{name: "sequential", mode: proxypool.ModeSequential},
		{name: "shuffled", mode: proxypool.ModeShuffled},
	} {
		b.Run(test.name, func(b *testing.B) {
			pool, err := proxypool.Open(path, proxypool.Options{Mode: test.mode, Seed: 1})
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			defer pool.Close()
			buffer := make([]byte, 0, len("proxy"))
			if buffer, err = pool.NextBytes(buffer); err != nil {
				b.Fatalf("first NextBytes: %v", err)
			}
			if buffer, err = pool.NextBytes(buffer[:0]); err != io.EOF {
				b.Fatalf("exhausting NextBytes = %v, want io.EOF", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if buffer, err = pool.NextBytes(buffer[:0]); err != io.EOF {
					b.Fatalf("NextBytes = %v, want io.EOF", err)
				}
			}
		})
	}
}

func BenchmarkReadCycle(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential"},
		{name: "shuffled", options: proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 1}},
		{name: "shuffled-region-per-block", options: proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   4 << 10,
			RegionBytes:  4 << 10,
			MaxLineBytes: 128,
			Seed:         1,
		}},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(info.Size())
			b.ReportAllocs()
			for b.Loop() {
				pool, err := proxypool.Open(path, test.options)
				if err != nil {
					b.Fatal(err)
				}
				buffer := make([]byte, 0, 128)
				for {
					buffer, err = pool.NextBytes(buffer[:0])
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				pool.Close()
			}
		})
	}
}

func BenchmarkSequentialBuffer(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{64 << 10, 256 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(info.Size())
			b.ReportAllocs()
			for b.Loop() {
				pool, err := proxypool.Open(path, proxypool.Options{SequentialBufferBytes: size})
				if err != nil {
					b.Fatal(err)
				}
				buffer := make([]byte, 0, 128)
				for {
					buffer, err = pool.NextBytes(buffer[:0])
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				pool.Close()
			}
		})
	}
}
