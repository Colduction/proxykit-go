//go:build windows

package blockread

import (
	"syscall"
	"unsafe"
)

// An Ahead keeps reads of a file in flight on a second, overlapped handle,
// each in a slot with storage the Ahead holds while the read runs, so that
// the storage stays reachable even when its caller is dropped. The cache
// manager serves a buffered read on its own threads; one read in flight
// measured slower than a synchronous handle on an uncached file, and two or
// more reached the throughput of the device.
//
// Without buffering, the device writes each block straight into the storage,
// which spares a copy per byte and the cache the pages of a scan that could
// never stay in it. Such a read must start and end on sector boundaries in
// memory and in the file.
type Ahead struct {
	requests [AheadDepth]request
	handle   syscall.Handle
	direct   bool
}

type request struct {
	overlapped syscall.Overlapped
	storage    []byte
	target     []byte
	block      int64
	offset     int64
	event      syscall.Handle
	done       uint32
	pending    bool
}

// Direct reports whether the reads of ahead bypass the system cache.
func (ahead *Ahead) Direct() bool {
	return ahead.direct
}

// Find returns the slot of the read of block in flight, or -1.
func (ahead *Ahead) Find(block int64) int {
	for slot := range ahead.requests {
		if request := &ahead.requests[slot]; request.pending && request.block == block {
			return slot
		}
	}
	return -1
}

// Idle returns a slot with no read in flight, or -1.
func (ahead *Ahead) Idle() int {
	for slot := range ahead.requests {
		if !ahead.requests[slot].pending {
			return slot
		}
	}
	return -1
}

// Block returns the block of the read in slot and whether it is in flight.
func (ahead *Ahead) Block(slot int) (int64, bool) {
	request := &ahead.requests[slot]
	return request.block, request.pending
}

// Swap exchanges the storage slot holds for storage and returns it. The
// slot must have no read in flight.
func (ahead *Ahead) Swap(slot int, storage []byte) []byte {
	request := &ahead.requests[slot]
	storage, request.storage = request.storage, storage
	return storage
}

// Start makes storage the storage of slot and reads target, which lies in
// storage, at offset for block. The slot must have no read in flight. A read
// that fails at once, such as one at or past the end of a file that shrank,
// leaves the slot without a read in flight; only other errors are returned.
func (ahead *Ahead) Start(slot int, block int64, storage, target []byte, offset int64) error {
	// The kernel writes target and the OVERLAPPED structure after ReadFile
	// returns; both are heap objects, which the collector does not move, and
	// the request keeps them reachable until Wait, as the Ahead keeps the
	// request. A runtime.Pinner would allocate as it passes between
	// processors. ReadFile stores a count only when it completes at once,
	// which Wait reads again; the race detector's wrapper needs a place for
	// it. Only a read that completed or is in flight signals the event; one
	// that failed at once leaves the OVERLAPPED structure pending, where a
	// wait would never return.
	request := &ahead.requests[slot]
	request.storage = storage
	request.overlapped = syscall.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(offset >> 32), HEvent: request.event}
	err := syscall.ReadFile(ahead.handle, target, &request.done, &request.overlapped)
	if err == errorHandleEOF {
		return nil
	}
	if err != nil && err != errorIOPending {
		return err
	}
	request.target, request.block, request.offset, request.pending = target, block, offset, true
	return nil
}

// Wait waits for the read in slot and reports its offset and length, the
// count it read, and its error, which is [io.EOF] for a count short of the
// length.
func (ahead *Ahead) Wait(slot int) (offset int64, length, read int, err error) {
	request := &ahead.requests[slot]
	r1, _, errno := syscall.SyscallN(procGetOverlappedResult.Addr(),
		uintptr(ahead.handle), uintptr(unsafe.Pointer(&request.overlapped)), uintptr(unsafe.Pointer(&request.done)), 1)
	length = len(request.target)
	request.target, request.pending = nil, false
	if r1 == 0 {
		err = errno
	}
	read, err = readResult(int(request.done), length, err)
	return request.offset, length, read, err
}

// Settle cancels the read in slot, if one is in flight, and waits until the
// kernel lets go of its storage.
func (ahead *Ahead) Settle(slot int) {
	request := &ahead.requests[slot]
	if !request.pending {
		return
	}
	_ = syscall.CancelIoEx(ahead.handle, &request.overlapped)
	_, _, _, _ = ahead.Wait(slot)
}

// RetainedBytes returns the memory the storage of ahead retains, which is 0
// for a nil Ahead.
func (ahead *Ahead) RetainedBytes() int64 {
	if ahead == nil {
		return 0
	}
	var total int64
	for slot := range ahead.requests {
		total += BufferBytes(cap(ahead.requests[slot].storage))
	}
	return total
}

// Close settles the reads of ahead and closes its handle and events. It
// suits a cleanup of the owner of ahead, which may run after the file's
// own cleanup closed the first handle; a wait then ends when the canceled
// read completes.
func (ahead *Ahead) Close() {
	for slot := range ahead.requests {
		ahead.Settle(slot)
		request := &ahead.requests[slot]
		request.storage = nil
		if request.event != 0 {
			_ = syscall.CloseHandle(request.event &^ 1)
			request.event = 0
		}
	}
	if ahead.handle != 0 {
		_ = syscall.CloseHandle(ahead.handle)
		ahead.handle = 0
	}
}
