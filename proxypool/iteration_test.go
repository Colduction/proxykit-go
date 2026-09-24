package proxypool_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go/proxypool"
)

func TestOpenRejectsAlignedBufferOverflow(t *testing.T) {
	if strconv.IntSize != 32 {
		t.Skip("uint32 offsets limit buffer sizes before int alignment can overflow")
	}
	maxInt := int(^uint(0) >> 1)
	path := writeFile(t, "proxy\n")
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		pool, err := proxypool.Open(path, proxypool.Options{
			Mode:                  mode,
			SequentialBufferBytes: 1,
			BlockBytes:            1,
			MaxLineBytes:          maxInt - 4,
		})
		if err == nil {
			pool.Close()
			t.Fatalf("mode %d: Open accepted a buffer whose alignment padding overflows int", mode)
		}
	}
}

func TestBatchLinesReplacementStopsBeforeNextYield(t *testing.T) {
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, ending := range []string{"\n", "\r\n"} {
			for _, count := range []int{1, 2, 3, 7} {
				for _, stop := range []bool{false, true} {
					for replaceAt := 1; replaceAt <= count; replaceAt++ {
						name := fmt.Sprintf("mode=%d/ending=%q/count=%d/stop=%v/replace=%d", mode, ending, count, stop, replaceAt)
						t.Run(name, func(t *testing.T) {
							path := writeFile(t, strings.Repeat("line"+ending, count))
							pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, Seed: 7, Reuse: true})
							if err != nil {
								t.Fatal(err)
							}
							defer pool.Close()
							var batch proxypool.Batch
							if err := pool.NextBatch(&batch); err != nil {
								t.Fatal(err)
							}
							var yielded int
							recovered := func() (value any) {
								defer func() { value = recover() }()
								for range batch.Lines() {
									yielded++
									if yielded == replaceAt {
										if err := pool.NextBatch(&batch); err != nil {
											t.Fatal(err)
										}
										if stop {
											break
										}
									}
								}
								return nil
							}()
							if recovered != "proxypool: Batch passed to NextBatch during Lines" {
								t.Fatalf("panic = %v, want batch replacement panic", recovered)
							}
							if yielded != replaceAt {
								t.Fatalf("yielded %d lines after replacing batch at %d", yielded, replaceAt)
							}
							if batch.Len() != count {
								t.Fatalf("replacement batch has %d lines, want %d", batch.Len(), count)
							}
						})
					}
				}
			}
		}
	}
}

func TestBatchLinesPanicAtEveryPosition(t *testing.T) {
	for _, mode := range []proxypool.Mode{proxypool.ModeSequential, proxypool.ModeShuffled} {
		for _, count := range []int{1, 2, 3, 7} {
			path, _ := makeProxyFile(t, count)
			pool, err := proxypool.Open(path, proxypool.Options{Mode: mode, Seed: 7, Reuse: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Close() })
			for panicAt := 1; panicAt <= count; panicAt++ {
				var batch proxypool.Batch
				if err := pool.NextBatch(&batch); err != nil {
					t.Fatal(err)
				}
				var before []string
				func() {
					defer func() {
						if recovered := recover(); recovered != "stop" {
							t.Fatalf("panic = %v, want stop", recovered)
						}
					}()
					for line := range batch.Lines() {
						before = append(before, string(line))
						if len(before) == panicAt {
							panic("stop")
						}
					}
				}()
				if batch.Len() != count {
					t.Fatalf("mode %d count %d panicAt %d: %d lines remain, want %d", mode, count, panicAt, batch.Len(), count)
				}
				var after []string
				for line := range batch.Lines() {
					after = append(after, string(line))
					if len(after) == panicAt {
						break
					}
				}
				if !slices.Equal(before, after) {
					t.Fatalf("mode %d count %d panicAt %d: replay = %q, want %q", mode, count, panicAt, after, before)
				}
			}
		}
	}
}
