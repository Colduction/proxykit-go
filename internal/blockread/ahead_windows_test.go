package blockread_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/colduction/proxykit-go/internal/blockread"
)

// An Ahead keeps reads in flight in slots that hold their storage; the tests
// run it buffered and without buffering, which needs page-aligned storage,
// offsets, and lengths.
func TestAhead(t *testing.T) {
	const blockBytes = 64 << 10
	content := pattern(10*blockBytes + 1234)
	path := writeFile(t, content)
	for _, direct := range []bool{false, true} {
		func() {
			defer blockread.SetForceDirect(blockread.SetForceDirect(direct))
			file, err := blockread.OpenSource(path, true)
			if err != nil {
				t.Fatalf("OpenSource: %v", err)
			}
			defer file.Close()
			var reader blockread.Reader
			reader.Init(file)
			ahead := reader.OpenAhead(int64(len(content)), true, direct)
			if ahead == nil {
				t.Fatal("OpenAhead = nil")
			}
			defer ahead.Close()
			if ahead.Direct() != direct {
				t.Fatalf("Direct = %v, want %v", ahead.Direct(), direct)
			}
			for block := int64(0); block < 11; block += blockread.AheadDepth {
				for i := range blockread.AheadDepth {
					slot := ahead.Idle()
					if slot < 0 {
						t.Fatalf("block %d: no idle slot", block)
					}
					storage := ahead.Swap(slot, nil)
					if storage == nil {
						storage = blockread.MakeBuffer(0, blockBytes+2*blockread.PageBytes)
					}
					storage = storage[:1+blockBytes]
					offset := (block + int64(i)) * blockBytes
					if err := ahead.Start(slot, block+int64(i), storage, storage[1:], offset); err != nil {
						t.Fatalf("Start(%d): %v", block+int64(i), err)
					}
				}
				for i := range blockread.AheadDepth {
					current := block + int64(i)
					slot := ahead.Find(current)
					start := current * blockBytes
					if start >= int64(len(content)) {
						// A read at the end of the file fails at once and
						// leaves no read in flight, or completes with nothing.
						if slot >= 0 {
							if _, _, read, err := ahead.Wait(slot); read != 0 || !errors.Is(err, io.EOF) {
								t.Fatalf("block %d: read past the end = %d, %v", current, read, err)
							}
						}
						continue
					}
					if slot < 0 {
						t.Fatalf("block %d: no read in flight", current)
					}
					if got, pending := ahead.Block(slot); got != current || !pending {
						t.Fatalf("Block(%d) = %d, %v", slot, got, pending)
					}
					offset, length, read, err := ahead.Wait(slot)
					want := min(blockBytes, len(content)-int(start))
					if offset != start || length != blockBytes || read != want || err != nil && !errors.Is(err, io.EOF) {
						t.Fatalf("block %d: Wait = %d, %d, %d, %v", current, offset, length, read, err)
					}
					storage := ahead.Swap(slot, nil)
					if !bytes.Equal(storage[1:1+read], content[start:start+int64(read)]) {
						t.Fatalf("block %d: wrong bytes", current)
					}
					ahead.Swap(slot, storage)
				}
			}
			slot := ahead.Idle()
			storage := blockread.MakeBuffer(1+blockBytes, blockBytes+2*blockread.PageBytes)
			if err := ahead.Start(slot, 1, storage, storage[1:], blockBytes); err != nil {
				t.Fatalf("Start: %v", err)
			}
			ahead.Settle(slot)
			if _, pending := ahead.Block(slot); pending {
				t.Fatal("a settled read is still in flight")
			}
			if ahead.RetainedBytes() == 0 {
				t.Fatal("RetainedBytes = 0 with storage in the slots")
			}
		}()
	}
}
