package proxypool

import (
	"iter"
	"math/bits"
	"unsafe"
)

// A Batch holds the lines of one block that [Pool.NextBatch] handed over.
// Its zero value is empty and ready for use.
//
// A Batch owns the block storage it holds and must not be copied after first
// use. It is not safe for concurrent use; give each goroutine its own. Lines
// that [Batch.Next] returns alias that storage and stay valid until the Batch
// is passed to [Pool.NextBatch] again, even after the pool is reset or closed.
// A returned line has no spare capacity, so appending to it copies it.
// Pass a Batch back to the pool that filled it, or to one with the same block
// and line limits, so that its storage is reused; the storage of a larger
// Batch is dropped instead.
type Batch struct {
	noCopy    noCopy
	remaining int
	next      int
	back      int
	lines     int
	calls     uint64
	offsets   []uint32
	buffer    []byte
	cr        bool
}

// Len returns the number of lines that [Batch.Next] has not returned yet.
func (batch *Batch) Len() int {
	return batch.remaining
}

// Next returns the next line of the batch and reports whether there was one.
// Lines come in the order [Pool.Next] would have returned them, without the
// trailing LF and an optional preceding CR. The line aliases the storage of
// the batch; see [Batch].
func (batch *Batch) Next() ([]byte, bool) {
	if batch.remaining <= 0 {
		return nil, false
	}
	batch.remaining--
	next := batch.next
	start, end := batch.offsets[next], batch.offsets[next+1]-1
	next -= batch.back
	batch.next = next + batch.lines&(next>>(bits.UintSize-1))
	if batch.cr && batch.buffer[end-1] == '\r' {
		end--
	}
	return batch.buffer[start:end:end], true
}

// Lines returns an iterator over the lines that [Batch.Next] has not
// returned yet, in the same order and form. It keeps its position in
// registers, which makes it cheaper per line than calling Next. When the loop
// stops early, the lines it did not reach remain for Next or another
// iteration; when its body panics, the batch keeps the position it had when
// the loop began. The body of the loop must not call Next, and it panics if
// the body passes the batch to [Pool.NextBatch].
func (batch *Batch) Lines() iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		next, remaining, back, lines := batch.next, batch.remaining, batch.back, batch.lines
		cr := batch.cr
		calls := batch.calls
		base := unsafe.Pointer(unsafe.SliceData(batch.buffer))
		ends := unsafe.Pointer(unsafe.SliceData(batch.offsets))
		if lines-back == 1 && remaining > 0 && remaining <= lines-next {
			start := offsetAt(ends, next)
			if !cr {
				for index := range remaining {
					stop := offsetAt(ends, next+1+index)
					line := view(base, start, stop-1)
					start = stop
					more := yield(line)
					batch.checkCalls(calls)
					if !more {
						batch.next, batch.remaining = next+1+index, remaining-1-index
						return
					}
				}
			} else {
				for index := range remaining {
					stop := offsetAt(ends, next+1+index)
					line := view(base, start, trimReturn(base, stop-1))
					start = stop
					more := yield(line)
					batch.checkCalls(calls)
					if !more {
						batch.next, batch.remaining = next+1+index, remaining-1-index
						return
					}
				}
			}
			batch.next, batch.remaining = next+remaining, 0
			return
		}
		step := lines - back
		twice := step + step - lines
		twice += lines & (twice >> (bits.UintSize - 1))
		backTwice := lines - twice
		first := next
		second := first - back
		second += lines & (second >> (bits.UintSize - 1))
		for ; remaining >= 2; remaining -= 2 {
			firstEnd, secondEnd := offsetAt(ends, first+1)-1, offsetAt(ends, second+1)-1
			if cr {
				firstEnd, secondEnd = trimReturn(base, firstEnd), trimReturn(base, secondEnd)
			}
			firstLine := view(base, offsetAt(ends, first), firstEnd)
			secondLine := view(base, offsetAt(ends, second), secondEnd)
			following := second
			first -= backTwice
			first += lines & (first >> (bits.UintSize - 1))
			second -= backTwice
			second += lines & (second >> (bits.UintSize - 1))
			more := yield(firstLine)
			batch.checkCalls(calls)
			if !more {
				batch.next, batch.remaining = following, remaining-1
				return
			}
			more = yield(secondLine)
			batch.checkCalls(calls)
			if !more {
				batch.next, batch.remaining = first, remaining-2
				return
			}
		}
		if remaining == 1 {
			end := offsetAt(ends, first+1) - 1
			if cr {
				end = trimReturn(base, end)
			}
			yield(view(base, offsetAt(ends, first), end))
			batch.checkCalls(calls)
			batch.next, batch.remaining = second, 0
			return
		}
		batch.next, batch.remaining = first, 0
	}
}

func (batch *Batch) checkCalls(calls uint64) {
	if batch.calls != calls {
		panic("proxypool: Batch passed to NextBatch during Lines")
	}
}

func offsetAt(ends unsafe.Pointer, index int) uint32 {
	return *(*uint32)(unsafe.Add(ends, uintptr(index)*4))
}

func view(base unsafe.Pointer, start, end uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Add(base, start)), end-start)
}

func trimReturn(base unsafe.Pointer, end uint32) uint32 {
	if *(*byte)(unsafe.Add(base, end-1)) == '\r' {
		end--
	}
	return end
}

// NextBatch fills batch with the lines of the next block and takes over the
// storage batch held, so that a Batch passed back on every call allocates
// nothing in steady state. When the pool has returned some lines of its
// current block through [Pool.Next] or [Pool.NextBytes], the batch receives
// the remaining ones. Lines given to a Batch count as returned in
// [Stats.Cursor].
//
// It returns [ErrNilBatch] for a nil batch and otherwise the errors of
// [Pool.Next], including [io.EOF] after exhaustion; on error the batch is
// empty.
func (pool *Pool) NextBatch(batch *Batch) error {
	if batch == nil {
		return ErrNilBatch
	}
	batch.remaining = 0
	batch.calls++
	if pool == nil {
		return ErrClosed
	}
	pool.mu.Lock()
	err := pool.nextBatchLocked(batch)
	pool.mu.Unlock()
	return err
}

func (pool *Pool) nextBatchLocked(batch *Batch) error {
	if pool.closed || pool.file == nil {
		return ErrClosed
	}
	if pool.terminal != nil {
		return pool.terminal
	}
	if cap(batch.buffer) > pool.blockBytes+pool.maxLineBytes+3 {
		batch.buffer = nil
	}
	if cap(batch.offsets) > pool.blockBytes+1 {
		batch.offsets = nil
	}
	lines := int64(len(pool.offsets)) - 1
	if pool.lineCursor >= lines {
		batch.buffer = pool.readAheadLocked(batch.buffer[:0])
		if err := pool.loadNextBlockLocked(); err != nil {
			pool.terminal = err
			return err
		}
		lines = int64(len(pool.offsets)) - 1
	}
	remaining := lines - pool.lineCursor
	pool.buffer, batch.buffer = batch.buffer[:0], pool.buffer
	pool.offsets, batch.offsets = batch.offsets[:0], pool.offsets
	batch.next, batch.back, batch.lines = int(pool.nextLine), int(lines-pool.lineStep), int(lines)
	batch.remaining, batch.cr = int(remaining), pool.cr
	pool.cursor += remaining
	pool.lineCursor, pool.nextLine, pool.cr = 0, 0, false
	return nil
}
