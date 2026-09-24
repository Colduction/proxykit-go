package proxypool

// BatchReindex rebuilds the line offsets of batch from its resident buffer
// with the pool's indexing routine and rewinds the batch to its first line,
// so that a benchmark measures indexing and iteration without a read.
// The block must have been handed over whole.
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

// BatchRewind makes batch return its lines again from permutation position
// first, which [BatchFirst] reported before the first call of [Batch.Next].
func BatchRewind(batch *Batch, first int) {
	batch.next, batch.remaining = first, len(batch.offsets)-1
}

// BatchFirst returns the permutation position of the next line of batch.
func BatchFirst(batch *Batch) int {
	return batch.next
}

// Direct reports whether pool reads ahead without buffering.
func Direct(pool *Pool) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.ahead != nil && pool.ahead.Direct()
}

// BatchOffsets returns the line offsets of batch for tests of the index.
func BatchOffsets(batch *Batch) []uint32 {
	return batch.offsets
}
