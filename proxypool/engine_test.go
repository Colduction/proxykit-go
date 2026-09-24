package proxypool_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

// TestOffsetsSlackAllLineFeeds fills blocks with nothing but line feeds, the
// most offsets a block can hold, at and around the sizes where the offsets
// reach their bound, so that indexing must progress with the least room.
func TestOffsetsSlackAllLineFeeds(t *testing.T) {
	for _, n := range []int{1, 63, 64, 65, 127, 128, 129, 4095, 4096, 4097} {
		path := writeFile(t, strings.Repeat("\n", n))
		for _, blockBytes := range []int{n, max(1, n-1), 64, 1 << 20} {
			for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
				pool, err := proxypool.Open(path, proxypool.Options{
					Mode:                  mode,
					SequentialBufferBytes: blockBytes,
					BlockBytes:            blockBytes,
					RegionBytes:           int64(blockBytes),
					MaxLineBytes:          16,
					Seed:                  5,
				})
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				got, err := collect(pool)
				stats := pool.Stats()
				pool.Close()
				if err != nil || len(got) != n {
					t.Fatalf("n=%d block=%d mode=%d: %d lines, %v", n, blockBytes, mode, len(got), err)
				}
				if stats.RetainedBytes > stats.MaxRetainedBytes {
					t.Fatalf("n=%d block=%d mode=%d: retained %d exceeds %d", n, blockBytes, mode, stats.RetainedBytes, stats.MaxRetainedBytes)
				}
			}
		}
	}
}

func TestSequentialMatchesFileOrderAcrossBlocks(t *testing.T) {
	path, want := makeProxyFile(t, 1_000)
	for _, blockBytes := range []int{1, 2, 3, 7, 16, 64, 4096, 1 << 20} {
		pool, err := proxypool.Open(path, proxypool.Options{SequentialBufferBytes: blockBytes, MaxLineBytes: 128})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		got, err := collect(pool)
		stats := pool.Stats()
		pool.Close()
		if err != nil {
			t.Fatalf("block=%d: %v", blockBytes, err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("block=%d: sequential order differs", blockBytes)
		}
		if stats.RetainedBytes > stats.MaxRetainedBytes {
			t.Fatalf("block=%d: retained %d exceeds %d", blockBytes, stats.RetainedBytes, stats.MaxRetainedBytes)
		}
	}
}

func TestSequentialLineShapesEveryBoundary(t *testing.T) {
	for _, content := range batchLayouts {
		for blockBytes := 1; blockBytes <= 17; blockBytes++ {
			pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{SequentialBufferBytes: blockBytes, MaxLineBytes: 64})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got, err := collect(pool)
			pool.Close()
			if err != nil {
				t.Fatalf("%q block=%d: %v", content, blockBytes, err)
			}
			if want := contentLines(content); !slices.Equal(got, want) {
				t.Fatalf("%q block=%d: lines = %#v, want %#v", content, blockBytes, got, want)
			}
		}
	}
}

// A shuffled block reads the byte before it only when it does not follow the
// block loaded before it; the other blocks take that byte from the previous
// load. Regions of three blocks mix both kinds of load at every boundary.
func TestShuffledLineShapesEveryBoundary(t *testing.T) {
	for _, content := range batchLayouts {
		for blockBytes := 1; blockBytes <= 17; blockBytes++ {
			for _, seed := range []uint64{1, 2, 3} {
				pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
					Mode:         proxypool.ModeShuffled,
					BlockBytes:   blockBytes,
					RegionBytes:  int64(3 * blockBytes),
					MaxLineBytes: 64,
					Seed:         seed,
				})
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				got, err := collect(pool)
				pool.Close()
				if err != nil {
					t.Fatalf("%q block=%d seed=%d: %v", content, blockBytes, seed, err)
				}
				want := contentLines(content)
				slices.Sort(got)
				slices.Sort(want)
				if !slices.Equal(got, want) {
					t.Fatalf("%q block=%d seed=%d: lines = %#v, want %#v", content, blockBytes, seed, got, want)
				}
			}
		}
	}
}

// TestSequentialErrorPoisonsBlock pins the error timing of the block engine:
// a line over the limit fails the block that holds it when the block loads,
// before the lines ahead of it in that block.
func TestSequentialErrorPoisonsBlock(t *testing.T) {
	content := "a\nb\n" + strings.Repeat("x", 70_000) + "\nc\n"
	pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	_, err = pool.Next()
	if !errors.Is(err, proxypool.ErrLineTooLong) {
		t.Fatalf("first Next = %v, want ErrLineTooLong", err)
	}
	if offset := errorOffset(t, err); offset != 4 {
		t.Fatalf("error offset = %d, want 4", offset)
	}
}

var errorOffsetPattern = regexp.MustCompile(`at byte (\d+)`)

func errorOffset(t *testing.T, err error) int64 {
	t.Helper()
	match := errorOffsetPattern.FindStringSubmatch(err.Error())
	if match == nil {
		t.Fatalf("error %q has no offset", err)
	}
	offset, parseErr := strconv.ParseInt(match[1], 10, 64)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	return offset
}

func TestLineTooLongReportsAbsoluteOffset(t *testing.T) {
	const limit = 8
	for _, prefix := range []string{"", "a\n", "abc\r\n", strings.Repeat("y\n", 20)} {
		content := prefix + strings.Repeat("x", limit+1) + "\nz\n"
		want := int64(len(prefix))
		for blockBytes := 1; blockBytes <= 17; blockBytes++ {
			for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
				pool, err := proxypool.Open(writeFile(t, content), proxypool.Options{
					Mode:                  mode,
					SequentialBufferBytes: blockBytes,
					BlockBytes:            blockBytes,
					RegionBytes:           int64(blockBytes),
					MaxLineBytes:          limit,
					Seed:                  11,
				})
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				_, err = collect(pool)
				pool.Close()
				if !errors.Is(err, proxypool.ErrLineTooLong) {
					t.Fatalf("%q block=%d mode=%d: %v, want ErrLineTooLong", content, blockBytes, mode, err)
				}
				if got := errorOffset(t, err); got != want {
					t.Fatalf("%q block=%d mode=%d: offset %d, want %d", content, blockBytes, mode, got, want)
				}
			}
		}
	}
}

// TestLineLimitFinalLine covers the final line of a file in one block, whose
// limit the gap of its offsets alone cannot decide.
func TestLineLimitFinalLine(t *testing.T) {
	const limit = 16
	tests := []struct {
		content string
		ok      bool
	}{
		{strings.Repeat("x", limit), true},
		{strings.Repeat("x", limit+1), false},
		{strings.Repeat("x", limit) + "\n", true},
		{strings.Repeat("x", limit) + "\r\n", true},
		{strings.Repeat("x", limit+1) + "\n", false},
		{strings.Repeat("x", limit-1) + "\r", true},
		{strings.Repeat("x", limit) + "\r", false},
		{"a\n" + strings.Repeat("x", limit) + "\r\n", true},
		{"a\n" + strings.Repeat("x", limit+1), false},
	}
	for _, test := range tests {
		for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
			pool, err := proxypool.Open(writeFile(t, test.content), proxypool.Options{Mode: mode, MaxLineBytes: limit, Seed: 3})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got, err := collect(pool)
			pool.Close()
			if test.ok {
				if err != nil || !slices.Equal(sortedCopy(got), sortedCopy(contentLines(test.content))) {
					t.Fatalf("%q mode=%d: %#v, %v", test.content, mode, got, err)
				}
			} else if !errors.Is(err, proxypool.ErrLineTooLong) {
				t.Fatalf("%q mode=%d: %v, want ErrLineTooLong", test.content, mode, err)
			}
		}
	}
}

func TestShortReadReportsSourceChanged(t *testing.T) {
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		path, _ := makeProxyFile(t, 200)
		pool, err := proxypool.Open(path, proxypool.Options{
			Mode:                  mode,
			SequentialBufferBytes: 1 << 10,
			BlockBytes:            1 << 10,
			RegionBytes:           1 << 10,
			MaxLineBytes:          128,
			Seed:                  2,
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err = os.Truncate(path, 1); err != nil {
			pool.Close()
			t.Skipf("cannot truncate an open file here: %v", err)
		}
		_, err = collect(pool)
		pool.Close()
		if !errors.Is(err, proxypool.ErrSourceChanged) {
			t.Fatalf("mode %d: %v, want ErrSourceChanged", mode, err)
		}
	}
}

// TestBatchLinesStopsAndResumes breaks out of Batch.Lines at every position
// and continues with Next and another Lines, which must together return the
// order Pool.Next returns.
func TestBatchLinesStopsAndResumes(t *testing.T) {
	path, _ := makeProxyFile(t, 300)
	for _, content := range []string{"", "crlf"} {
		source := path
		if content == "crlf" {
			source = writeFile(t, strings.ReplaceAll(readFile(t, path), "\n", "\r\n"))
		}
		for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
			options := proxypool.Options{Mode: mode, SequentialBufferBytes: 4 << 10, BlockBytes: 4 << 10, RegionBytes: 8 << 10, MaxLineBytes: 128, Seed: 9}
			reference, err := proxypool.Open(source, options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			want, err := collect(reference)
			reference.Close()
			if err != nil {
				t.Fatalf("collect: %v", err)
			}
			for stop := 0; stop < 70; stop += 7 {
				pool, err := proxypool.Open(source, options)
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				var (
					got   []string
					batch proxypool.Batch
				)
				for {
					if err := pool.NextBatch(&batch); err != nil {
						if !errors.Is(err, io.EOF) {
							t.Fatalf("NextBatch: %v", err)
						}
						break
					}
					taken := 0
					for line := range batch.Lines() {
						got = append(got, string(line))
						if taken++; taken == stop {
							break
						}
					}
					if line, ok := batch.Next(); ok {
						got = append(got, string(line))
					}
					for line := range batch.Lines() {
						got = append(got, string(line))
					}
					if batch.Len() != 0 {
						t.Fatalf("Len = %d after draining", batch.Len())
					}
				}
				pool.Close()
				if !slices.Equal(got, want) {
					t.Fatalf("%s mode=%d stop=%d: order differs from Next", content, mode, stop)
				}
			}
		}
	}
}

func ExamplePool_NextBatch() {
	path := writeExampleFile("http://192.0.2.1:8080\nsocks5://alice:secret@203.0.113.7:1080\r\n")
	defer os.Remove(path)
	pool, err := proxypool.Open(path, proxypool.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer pool.Close()
	var batch proxypool.Batch
	for {
		if err := pool.NextBatch(&batch); err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Println(err)
			}
			break
		}
		for line := range batch.Lines() {
			fmt.Println(string(line))
		}
	}
	// Output:
	// http://192.0.2.1:8080
	// socks5://alice:secret@203.0.113.7:1080
}

func writeExampleFile(content string) string {
	file, err := os.CreateTemp("", "proxies-*.txt")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		panic(err)
	}
	return file.Name()
}
