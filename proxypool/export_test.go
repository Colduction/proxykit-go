package proxypool

// BatchReindex rebuilds the supplied batch's line offsets from its resident
// buffer and rewinds to the requested permutation position. The batch must
// contain a complete transferred block. Benchmarks can use it to measure
// indexing and iteration without a read.
func BatchReindex(batch *Batch, first int) {
	lineStart, end := int(batch.offsets[0]), int(batch.offsets[len(batch.offsets)-1])
	pool := Pool{
		buffer:       batch.buffer,
		offsets:      batch.offsets[:0],
		blockBytes:   len(batch.buffer),
		maxLineBytes: len(batch.buffer),
		fileSize:     int64(len(batch.buffer)),
	}
	pool.appendOffset(uint32(lineStart))
	pool.indexBlockLocked(lineStart, end)
	if err := pool.checkLineLimitLocked(0); err != nil {
		panic(err)
	}
	step := batch.lines - batch.back
	batch.offsets, batch.cr, batch.lines = pool.offsets, pool.cr, len(pool.offsets)-1
	batch.back = batch.lines - step
	BatchRewind(batch, first)
}

// BatchRewind makes the supplied batch return its lines again from a
// permutation position reported by [BatchFirst] before the first [Batch.Next] call.
func BatchRewind(batch *Batch, first int) {
	batch.next, batch.remaining = first, len(batch.offsets)-1
}

// BatchFirst returns the permutation position of the supplied batch's next line.
func BatchFirst(batch *Batch) int {
	return batch.next
}

// Direct reports whether the supplied pool reads ahead without buffering.
func Direct(pool *Pool) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.ahead != nil && pool.ahead.Direct()
}

// BatchOffsets returns the supplied batch's line offsets for index tests.
// The returned slice aliases batch-owned storage.
func BatchOffsets(batch *Batch) []uint32 {
	return batch.offsets
}
