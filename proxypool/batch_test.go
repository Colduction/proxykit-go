package proxypool_test

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func collectBatches(pool *proxypool.Pool, limit int) ([]string, error) {
	// It drains pool through NextBatch with one reused Batch and
	// returns the lines in emission order, stopping after limit lines when limit
	// is positive.
	var (
		lines []string
		batch proxypool.Batch
	)
	for limit <= 0 || len(lines) < limit {
		if err := pool.NextBatch(&batch); err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
		if batch.Len() == 0 {
			return lines, errors.New("NextBatch returned an empty batch")
		}
		count := 0
		for {
			line, ok := batch.Next()
			if !ok {
				break
			}
			count++
			lines = append(lines, string(line))
		}
		if _, ok := batch.Next(); ok {
			return lines, errors.New("Next returned a line after reporting exhaustion")
		}
		if batch.Len() != 0 {
			return lines, fmt.Errorf("Len = %d after exhaustion", batch.Len())
		}
		if count == 0 {
			return lines, errors.New("batch had lines but Next returned none")
		}
	}
	return lines, nil
}

func collectLimited(pool *proxypool.Pool, limit int) ([]string, error) {
	var lines []string
	for limit <= 0 || len(lines) < limit {
		line, err := pool.Next()
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

var batchLayouts = []string{
	"",
	"\n",
	"\n\n",
	"a",
	"a\n",
	"a\nb",
	"a\r\nb\n\nc\r",
	"0123456789abcdef\nshort\nlast",
	strings.Repeat("z", 31) + "\r\nend\n",
	strings.Repeat("line\r\n", 40) + "tail\r",
}

func TestNextBatchMatchesNext(t *testing.T) {
	proxyPath, proxyLines := makeProxyFile(t, 1_000)
	contents := append(slices.Clone(batchLayouts), strings.Join(proxyLines, "\n")+"\n")
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for index, content := range contents {
			path := writeFile(t, content)
			if index == len(contents)-1 {
				path = proxyPath
			}
			for _, blockBytes := range []int{1, 3, 7, 16, 64, 4096} {
				for _, reuse := range []bool{false, true} {
					options := proxypool.Options{
						Mode:                  mode,
						SequentialBufferBytes: blockBytes,
						BlockBytes:            blockBytes,
						RegionBytes:           int64(blockBytes * 3),
						MaxLineBytes:          128,
						Seed:                  0x5eed,
						Reuse:                 reuse,
					}
					want := contentLines(content)
					limit := 0
					if reuse {
						limit = 3 * len(want)
					}
					name := fmt.Sprintf("mode=%d/content=%d/block=%d/reuse=%v", mode, index, blockBytes, reuse)
					t.Run(name, func(t *testing.T) {
						reference, err := proxypool.Open(path, options)
						if err != nil {
							t.Fatalf("Open: %v", err)
						}
						defer reference.Close()
						batched, err := proxypool.Open(path, options)
						if err != nil {
							t.Fatalf("Open: %v", err)
						}
						defer batched.Close()
						wantLines, err := collectLimited(reference, limit)
						if err != nil {
							t.Fatalf("Next: %v", err)
						}
						got, err := collectBatches(batched, limit)
						if err != nil {
							t.Fatalf("NextBatch: %v", err)
						}
						if !slices.Equal(got, wantLines) {
							t.Fatalf("batch lines = %#v, want %#v", got, wantLines)
						}
						if !reuse && !slices.Equal(sortedCopy(got), sortedCopy(want)) {
							t.Fatalf("lines = %#v, want content %#v", got, want)
						}
						if reference.Stats().Cursor != batched.Stats().Cursor {
							t.Fatalf("Cursor = %d, want %d", batched.Stats().Cursor, reference.Stats().Cursor)
						}
					})
				}
			}
		}
	}
}

func TestNextBatchMatchesNextAcrossShards(t *testing.T) {
	const shardCount = 3
	path, want := makeProxyFile(t, 700)
	var all []string
	for shardIndex := range shardCount {
		options := proxypool.Options{
			Mode:         proxypool.ModeShuffled,
			BlockBytes:   128,
			RegionBytes:  384,
			MaxLineBytes: 128,
			Seed:         4242,
			ShardCount:   shardCount,
			ShardIndex:   shardIndex,
		}
		reference, err := proxypool.Open(path, options)
		if err != nil {
			t.Fatalf("Open shard %d: %v", shardIndex, err)
		}
		wantLines, err := collect(reference)
		reference.Close()
		if err != nil {
			t.Fatalf("collect shard %d: %v", shardIndex, err)
		}
		batched, err := proxypool.Open(path, options)
		if err != nil {
			t.Fatalf("Open shard %d: %v", shardIndex, err)
		}
		got, err := collectBatches(batched, 0)
		batched.Close()
		if err != nil {
			t.Fatalf("NextBatch shard %d: %v", shardIndex, err)
		}
		if !slices.Equal(got, wantLines) {
			t.Fatalf("shard %d order differs", shardIndex)
		}
		all = append(all, got...)
	}
	if !slices.Equal(sortedCopy(all), sortedCopy(want)) {
		t.Fatalf("shards returned %d lines, want %d exact lines", len(all), len(want))
	}
}

func TestNextBatchMixedCallsExactOnce(t *testing.T) {
	path, want := makeProxyFile(t, 2_000)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			pool, err := proxypool.Open(path, proxypool.Options{
				Mode:                  mode,
				SequentialBufferBytes: 1 << 10,
				BlockBytes:            1 << 10,
				RegionBytes:           4 << 10,
				MaxLineBytes:          128,
				Seed:                  77,
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer pool.Close()
			random := rand.New(rand.NewPCG(1, 2))
			var (
				got    []string
				batch  proxypool.Batch
				buffer []byte
			)
			for {
				var err error
				switch random.IntN(3) {
				case 0:
					var line string
					line, err = pool.Next()
					if err == nil {
						got = append(got, line)
					}
				case 1:
					buffer, err = pool.NextBytes(buffer[:0])
					if err == nil {
						got = append(got, string(buffer))
					}
				default:
					err = pool.NextBatch(&batch)
					for err == nil {
						line, ok := batch.Next()
						if !ok {
							break
						}
						got = append(got, string(line))
					}
				}
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read: %v", err)
				}
			}
			if mode == proxypool.ModeSequential {
				if !slices.Equal(got, want) {
					t.Fatalf("sequential mixed calls broke file order")
				}
			} else if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
				t.Fatalf("got %d lines, want %d exact lines", len(got), len(want))
			}
			if cursor := pool.Stats().Cursor; cursor != int64(len(want)) {
				t.Fatalf("Cursor = %d, want %d", cursor, len(want))
			}
		})
	}
}

func TestNextBatchConcurrentExactOnce(t *testing.T) {
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
			var batch proxypool.Batch
			for {
				err := pool.NextBatch(&batch)
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					errorsFound <- err
					return
				}
				for {
					line, ok := batch.Next()
					if !ok {
						break
					}
					lines <- string(line)
				}
			}
		})
	}
	wait.Wait()
	close(lines)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("NextBatch: %v", err)
	}
	got := make([]string, 0, len(lines))
	for line := range lines {
		got = append(got, line)
	}
	if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
		t.Fatalf("got %d lines, want %d exact lines", len(got), len(want))
	}
}

func TestNextBatchZeroAllocation(t *testing.T) {
	path, _ := makeProxyFile(t, 400)
	for _, test := range []struct {
		name    string
		options proxypool.Options
	}{
		{name: "sequential", options: proxypool.Options{Reuse: true, SequentialBufferBytes: 8 << 10}},
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
			if blocks := pool.Stats().Blocks; blocks < 2 {
				t.Fatalf("Blocks = %d, want at least 2", blocks)
			}
			var batch proxypool.Batch
			drain := func() {
				if err := pool.NextBatch(&batch); err != nil {
					panic(err)
				}
				for {
					if _, ok := batch.Next(); !ok {
						break
					}
				}
			}
			for range 6 {
				drain()
			}
			if allocations := testing.AllocsPerRun(200, drain); allocations != 0 {
				t.Fatalf("allocations = %.2f, want 0", allocations)
			}
		})
	}
}

func TestNextBatchErrorsLeaveBatchEmpty(t *testing.T) {
	path := writeFile(t, "a\nb\n")
	pool, err := proxypool.Open(path, proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if err = pool.NextBatch(nil); !errors.Is(err, proxypool.ErrNilBatch) {
		t.Fatalf("NextBatch(nil) = %v, want ErrNilBatch", err)
	}
	var batch proxypool.Batch
	if err = pool.NextBatch(&batch); err != nil || batch.Len() != 2 {
		t.Fatalf("NextBatch = %v, Len = %d", err, batch.Len())
	}
	if err = pool.NextBatch(&batch); !errors.Is(err, io.EOF) || batch.Len() != 0 {
		t.Fatalf("exhausted NextBatch = %v, Len = %d; want io.EOF, 0", err, batch.Len())
	}
	if _, ok := batch.Next(); ok {
		t.Fatal("Next returned a line from an empty batch")
	}
	if err = pool.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err = pool.NextBatch(&batch); err != nil || batch.Len() != 2 {
		t.Fatalf("NextBatch after Reset = %v, Len = %d", err, batch.Len())
	}

	long, err := proxypool.Open(writeFile(t, strings.Repeat("x", 65)+"\nok\n"), proxypool.Options{MaxLineBytes: 64})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer long.Close()
	for range 2 {
		if err = long.NextBatch(&batch); !errors.Is(err, proxypool.ErrLineTooLong) || batch.Len() != 0 {
			t.Fatalf("NextBatch = %v, Len = %d; want terminal ErrLineTooLong, 0", err, batch.Len())
		}
	}

	pool.Close()
	if err = pool.NextBatch(&batch); !errors.Is(err, proxypool.ErrClosed) || batch.Len() != 0 {
		t.Fatalf("closed NextBatch = %v, Len = %d", err, batch.Len())
	}
	var nilPool *proxypool.Pool
	if err = nilPool.NextBatch(&batch); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("nil NextBatch = %v", err)
	}
	if err = new(proxypool.Pool).NextBatch(&batch); !errors.Is(err, proxypool.ErrClosed) {
		t.Fatalf("zero NextBatch = %v", err)
	}
}

func TestBatchOutlivesCloseAndReset(t *testing.T) {
	path, want := makeProxyFile(t, 50)
	for _, closeFirst := range []bool{true, false} {
		pool, err := proxypool.Open(path, proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 9})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var batch proxypool.Batch
		if err = pool.NextBatch(&batch); err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if closeFirst {
			pool.Close()
		} else if err = pool.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		var got []string
		for {
			line, ok := batch.Next()
			if !ok {
				break
			}
			got = append(got, string(line))
		}
		if !slices.Equal(sortedCopy(got), sortedCopy(want)) {
			t.Fatalf("closeFirst=%v: batch lines = %d, want %d", closeFirst, len(got), len(want))
		}
		err = pool.NextBatch(&batch)
		if closeFirst && !errors.Is(err, proxypool.ErrClosed) {
			t.Fatalf("NextBatch after Close = %v, want ErrClosed", err)
		}
		if !closeFirst && (err != nil || batch.Len() != len(want)) {
			t.Fatalf("NextBatch after Reset = %v, Len = %d", err, batch.Len())
		}
		pool.Close()
	}
}

func TestBatchStorageFromLargerPoolIsDropped(t *testing.T) {
	path, _ := makeProxyFile(t, benchmarkLines)
	large, err := proxypool.Open(path, proxypool.Options{Mode: proxypool.ModeShuffled, BlockBytes: 256 << 10, Seed: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer large.Close()
	var batch proxypool.Batch
	if err = large.NextBatch(&batch); err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	small, err := proxypool.Open(path, proxypool.Options{Mode: proxypool.ModeShuffled, BlockBytes: 4 << 10, RegionBytes: 4 << 10, MaxLineBytes: 128, Seed: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer small.Close()
	for range 3 {
		if err = small.NextBatch(&batch); err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
	}
	if stats := small.Stats(); stats.RetainedBytes > stats.MaxRetainedBytes {
		t.Fatalf("retained %d exceeds maximum %d after adopting larger storage", stats.RetainedBytes, stats.MaxRetainedBytes)
	}
}

func TestStatsCursorCountsBatchLines(t *testing.T) {
	path, want := makeProxyFile(t, 100)
	pool, err := proxypool.Open(path, proxypool.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	for range 3 {
		if _, err = pool.Next(); err != nil {
			t.Fatalf("Next: %v", err)
		}
	}
	var batch proxypool.Batch
	if err = pool.NextBatch(&batch); err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch.Len() != len(want)-3 {
		t.Fatalf("Len = %d, want %d", batch.Len(), len(want)-3)
	}
	if cursor := pool.Stats().Cursor; cursor != int64(len(want)) {
		t.Fatalf("Cursor = %d, want %d", cursor, len(want))
	}
	line, ok := batch.Next()
	if !ok || string(line) != want[3] {
		t.Fatalf("first batch line = %q, want %q", line, want[3])
	}
}

func TestBatchReindexReproducesOffsets(t *testing.T) {
	path, _ := makeProxyFile(t, 1_000)
	for _, content := range append(slices.Clone(batchLayouts), "") {
		for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
			source := path
			if content != "" {
				source = writeFile(t, content)
			}
			pool, err := proxypool.Open(source, proxypool.Options{Mode: mode, SequentialBufferBytes: 64, BlockBytes: 64, RegionBytes: 192, MaxLineBytes: 128, Seed: 3})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			var batch proxypool.Batch
			for {
				if err := pool.NextBatch(&batch); err != nil {
					if !errors.Is(err, io.EOF) {
						t.Fatalf("NextBatch: %v", err)
					}
					break
				}
				first := proxypool.BatchFirst(&batch)
				var want []string
				for {
					line, ok := batch.Next()
					if !ok {
						break
					}
					want = append(want, string(line))
				}
				offsets := slices.Clone(proxypool.BatchOffsets(&batch))
				proxypool.BatchReindex(&batch, first)
				if got := proxypool.BatchOffsets(&batch); !slices.Equal(got, offsets) {
					t.Fatalf("reindexed offsets = %v, want %v", got, offsets)
				}
				var got []string
				for {
					line, ok := batch.Next()
					if !ok {
						break
					}
					got = append(got, string(line))
				}
				if !slices.Equal(got, want) {
					t.Fatalf("reindexed lines = %#v, want %#v", got, want)
				}
			}
			pool.Close()
		}
	}
}

// TestBatchNextInlines asserts that the compiler inlines Batch.Next, whose
// per-line cost is the point of the batch API.
func TestBatchNextInlines(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the package")
	}
	command := exec.Command("go", "build", "-gcflags=-m", ".")
	command.Env = append(os.Environ(), "GOFLAGS=")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	for _, name := range []string{"(*Batch).Next", "(*Batch).Len"} {
		if !strings.Contains(string(output), "can inline "+name) {
			t.Errorf("%s is not inlinable:\n%s", name, output)
		}
	}
}

func FuzzNextBatchMatchesNext(f *testing.F) {
	for _, layout := range batchLayouts {
		f.Add([]byte(layout), uint8(3), uint64(1))
	}
	f.Fuzz(func(t *testing.T, content []byte, blockBytes uint8, seed uint64) {
		path := writeFile(t, string(content))
		for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
			options := proxypool.Options{
				Mode:                  mode,
				SequentialBufferBytes: int(blockBytes) + 1,
				BlockBytes:            int(blockBytes) + 1,
				RegionBytes:           int64(blockBytes)*2 + 2,
				MaxLineBytes:          512,
				Seed:                  seed | 1,
			}
			reference, err := proxypool.Open(path, options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			want, wantErr := collect(reference)
			reference.Close()
			batched, err := proxypool.Open(path, options)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got, gotErr := collectBatches(batched, 0)
			batched.Close()
			if !errors.Is(gotErr, wantErr) && (gotErr == nil || wantErr == nil || gotErr.Error() != wantErr.Error()) {
				t.Fatalf("mode %d: NextBatch error %v, Next error %v", mode, gotErr, wantErr)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("mode %d: batch lines %#v, want %#v", mode, got, want)
			}
		}
	})
}

var sink int

func drainBatch(batch *proxypool.Batch) int {
	total := 0
	for {
		line, ok := batch.Next()
		if !ok {
			return total
		}
		total += int(line[0])
	}
}

func BenchmarkNextBatch(b *testing.B) {
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
			var batch proxypool.Batch
			// Two cycles warm the pool and the batch to their block capacities,
			// so that the loop measures the steady state.
			for range 8 {
				if err := pool.NextBatch(&batch); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			lines := 0
			for b.Loop() {
				if err := pool.NextBatch(&batch); err != nil {
					b.Fatal(err)
				}
				lines += batch.Len()
				sink += drainBatch(&batch)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(lines), "ns/line")
		})
	}
}

// The iteration benchmarks measure full blocks of the benchmark file at the
// default block sizes, 1 MiB sequential and 4 MiB shuffled, which is the
// cache footprint a pool works on right after it reads a block. Shuffled
// blocks come from four seeds, since the cost of visiting a block in permuted
// order depends on the permutation step.
var (
	defaultSequential = proxypool.Options{}
	defaultShuffled   = proxypool.Options{Mode: proxypool.ModeShuffled, Seed: 1}
)

// iterated is a block loaded once, with the permutation position of its
// first line and its line count.
type iterated struct {
	batch *proxypool.Batch
	name  string
	first int
	lines int
}

// BenchmarkBatchIterate measures the cost the pool adds per line once a block
// is in memory: indexing the block and iterating its lines, through
// Batch.Lines and through Batch.Next, with the first byte of every line read.
// It is the measure of the ten-fold goal against BenchmarkNextBytes before the
// block engine.
func BenchmarkBatchIterate(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	crlfPath := writeFile(b, strings.ReplaceAll(readFile(b, path), "\n", "\r\n"))
	for _, mode := range []string{"sequential", "shuffled"} {
		for _, ending := range []string{"lf", "crlf"} {
			source := path
			if ending == "crlf" {
				source = crlfPath
			}
			for _, block := range loadBlocks(b, source, mode) {
				for _, iterator := range []string{"lines", "next"} {
					b.Run(mode+"/"+ending+"/"+iterator+block.name, func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							proxypool.BatchReindex(block.batch, block.first)
							if iterator == "lines" {
								sink += drainLines(block.batch)
							} else {
								sink += drainBatch(block.batch)
							}
						}
						b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(block.lines), "ns/line")
					})
				}
			}
		}
	}
}

func drainLines(batch *proxypool.Batch) int {
	total := 0
	for line := range batch.Lines() {
		total += int(line[0])
	}
	return total
}

// BenchmarkBatchLines measures iteration alone through Batch.Lines.
func BenchmarkBatchLines(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	for _, mode := range []string{"sequential", "shuffled"} {
		for _, block := range loadBlocks(b, path, mode) {
			for _, iterator := range []string{"lines", "next"} {
				b.Run(mode+"/"+iterator+block.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						proxypool.BatchRewind(block.batch, block.first)
						if iterator == "lines" {
							sink += drainLines(block.batch)
						} else {
							sink += drainBatch(block.batch)
						}
					}
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(block.lines), "ns/line")
				})
			}
		}
	}
}

func loadBlocks(b *testing.B, path, mode string) []iterated {
	// It returns the largest block of path at the default options of
	// mode, or of four seeds in shuffled mode.
	b.Helper()
	seeds := []uint64{0}
	options := defaultSequential
	if mode == "shuffled" {
		seeds = []uint64{1, 2, 3, 4}
		options = defaultShuffled
	}
	var blocks []iterated
	for _, seed := range seeds {
		options.Seed = seed
		pool, err := proxypool.Open(path, options)
		if err != nil {
			b.Fatal(err)
		}
		var largest *proxypool.Batch
		for {
			batch := new(proxypool.Batch)
			err := pool.NextBatch(batch)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			if largest == nil || batch.Len() > largest.Len() {
				largest = batch
			}
		}
		pool.Close()
		if largest == nil || largest.Len() < 1_000 {
			b.Fatal("no full block")
		}
		name := ""
		if mode == "shuffled" {
			name = fmt.Sprintf("/seed=%d", seed)
		}
		blocks = append(blocks, iterated{batch: largest, name: name, first: proxypool.BatchFirst(largest), lines: largest.Len()})
	}
	return blocks
}

func readFile(tb testing.TB, path string) string {
	tb.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return string(content)
}

// BenchmarkReadAtFloor measures the kernel copy of the whole file through the
// same handle flags the pool uses, which no user-space change can beat.
func BenchmarkReadAtFloor(b *testing.B) {
	path, _ := makeProxyFile(b, benchmarkLines)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		mode  proxypool.Mode
		block int
	}{
		{name: "sequential", mode: proxypool.ModeSequential, block: proxypool.DefaultSequentialBufferBytes},
		{name: "shuffled", mode: proxypool.ModeShuffled, block: proxypool.DefaultBlockBytes},
	} {
		b.Run(test.name, func(b *testing.B) {
			file, err := proxypool.OpenFile(path, os.O_RDONLY, 0, test.mode)
			if err != nil {
				b.Fatal(err)
			}
			defer file.Close()
			buffer := make([]byte, test.block)
			b.SetBytes(info.Size())
			b.ReportAllocs()
			for b.Loop() {
				for offset := int64(0); offset < info.Size(); offset += int64(test.block) {
					n, err := file.ReadAt(buffer, offset)
					if err != nil && !errors.Is(err, io.EOF) {
						b.Fatal(err)
					}
					sink += n
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(benchmarkLines), "ns/line")
		})
	}
}

func BenchmarkReadCycleBatch(b *testing.B) {
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
					sink += drainBatch(&batch)
				}
				pool.Close()
			}
		})
	}
}

// The views of Lines assume that the batch keeps its storage while the loop
// runs, so passing the batch to NextBatch in the body must panic, on the
// linear path of sequential pools and the permuted path of shuffled ones.
func TestBatchLinesPanicsOnNextBatchInBody(t *testing.T) {
	path, _ := makeProxyFile(t, 2_000)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, SequentialBufferBytes: 4 << 10, BlockBytes: 4 << 10, RegionBytes: 16 << 10, Seed: 3})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var batch proxypool.Batch
		if err := pool.NextBatch(&batch); err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		recovered := func() (value any) {
			defer func() { value = recover() }()
			for range batch.Lines() {
				_ = pool.NextBatch(&batch)
			}
			return nil
		}()
		pool.Close()
		if recovered == nil {
			t.Fatalf("mode %d: Lines did not panic after NextBatch in its body", mode)
		}
	}
}

// A body that panics leaves the batch where the loop began, so a later loop
// yields the same lines again.
func TestBatchLinesPanicKeepsPosition(t *testing.T) {
	path, _ := makeProxyFile(t, 500)
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, Seed: 7})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var batch proxypool.Batch
		if err := pool.NextBatch(&batch); err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		before := batch.Len()
		var first []string
		func() {
			defer func() { _ = recover() }()
			for line := range batch.Lines() {
				if first = append(first, string(line)); len(first) == 3 {
					panic("stop")
				}
			}
		}()
		if batch.Len() != before {
			t.Fatalf("mode %d: Len = %d after a panic, want %d", mode, batch.Len(), before)
		}
		var again []string
		for line := range batch.Lines() {
			if again = append(again, string(line)); len(again) == 3 {
				break
			}
		}
		pool.Close()
		if !slices.Equal(first, again) {
			t.Fatalf("mode %d: lines after a panic = %q, want %q", mode, again, first)
		}
	}
}
