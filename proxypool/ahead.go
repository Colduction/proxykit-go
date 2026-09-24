package proxypool

import (
	"io"
	"slices"

	"github.com/colduction/proxykit-go/internal/blockread"
)

func (pool *Pool) readAheadLocked(spare []byte) []byte {
	ahead := pool.ahead
	if !pool.prefetching || ahead == nil {
		return spare
	}
	var blocks [blockread.AheadDepth]int64
	upcoming := blocks[:pool.upcomingBlocksLocked(blocks[:])]
	for slot := range blockread.AheadDepth {
		if block, pending := ahead.Block(slot); pending && !slices.Contains(upcoming, block) {
			ahead.Settle(slot)
		}
	}
	carried := pool.carriedBlock
	for _, block := range upcoming {
		readOffset, skip, _, readBytes := pool.blockReadRange(block, carried)
		carried = block + 1
		if ahead.Find(block) >= 0 {
			continue
		}
		slot := ahead.Idle()
		storage := ahead.Swap(slot, nil)
		switch {
		case cap(storage) > 0:
		case cap(spare) > 0:
			storage, spare = spare, nil
		default:
			storage, pool.buffer = pool.buffer, nil
		}
		offset, length := readOffset+int64(skip), readBytes
		if ahead.Direct() {
			offset, length = readOffset+1, 1+(readBytes-1+blockread.PageBytes-1)&^(blockread.PageBytes-1)
		}
		storage = pool.resized(storage[:0], length)
		target := storage[skip:length]
		if ahead.Direct() {
			if !blockread.PageAligned(storage[1:]) {
				storage = pool.resized(nil, length)
			}
			target = storage[1:length]
		}
		if ahead.Start(slot, block, storage, target, offset) != nil {
			pool.prefetching = false
			return spare
		}
	}
	return spare
}

func (pool *Pool) adoptLocked(block, offset int64, skip, readBytes int) (int, bool, error) {
	ahead := pool.ahead
	if ahead == nil {
		return 0, false, nil
	}
	slot := ahead.Find(block)
	if slot < 0 {
		return 0, false, nil
	}
	issued, length, read, err := ahead.Wait(slot)
	failed := err != nil && err != io.EOF
	if !ahead.Direct() {
		if failed || issued != offset || length != readBytes-skip || read < length {
			return 0, false, nil
		}
		pool.buffer = ahead.Swap(slot, pool.buffer[:0])[:readBytes]
		return read, true, nil
	}
	blockStart := offset + 1 - int64(skip)
	if failed || issued != blockStart || read < readBytes-1 {
		return 0, false, nil
	}
	pool.buffer = ahead.Swap(slot, pool.buffer[:0])[:readBytes]
	if skip == 0 {
		if read, err := pool.reader.ReadAt(pool.buffer[:1], blockStart-1); read != 1 {
			return read, true, err
		}
	}
	return readBytes - skip, true, nil
}
