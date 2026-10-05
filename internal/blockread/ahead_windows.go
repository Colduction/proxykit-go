//go:build windows

package blockread

import (
	"syscall"
	"unsafe"
)

// An Ahead keeps reads of a file in flight on a separate, overlapped handle.
// [Reader.OpenAhead] creates it; the zero value owns no resources.
// Each slot retains its storage until [Ahead.Swap] or [Ahead.Close] releases it.
// Reads in flight must complete through [Ahead.Wait], [Ahead.Settle], or
// [Ahead.Close] before their storage is accessed or replaced.
// Slot arguments must be between zero and [AheadDepth] minus one.
//
// Reads that bypass the system cache require sector-aligned buffer addresses,
// file offsets, and lengths. Aligning each to [PageBytes] satisfies these
// requirements for supported files.
type Ahead struct {
	requests [AheadDepth]request
	handle   syscall.Handle
	direct   bool
}

type request struct {
	storage    []byte
	target     []byte
	overlapped syscall.Overlapped
	block      int64
	offset     int64
	event      syscall.Handle
	done       uint32
	pending    bool
}

// Direct reports whether reads bypass the system cache.
func (ahead *Ahead) Direct() bool {
	return ahead.direct
}

// Find returns the slot with a read in flight for the requested block, or -1.
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

// Block returns the slot's block number and reports whether its read is in flight.
func (ahead *Ahead) Block(slot int) (int64, bool) {
	request := &ahead.requests[slot]
	return request.block, request.pending
}

// Swap replaces the slot's storage and returns its previous storage.
// The slot must have no read in flight.
func (ahead *Ahead) Swap(slot int, storage []byte) []byte {
	request := &ahead.requests[slot]
	storage, request.storage = request.storage, storage
	return storage
}

// Start submits a read into the target buffer at the requested byte offset
// and associates it with the slot and block number.
// The target must lie within the supplied storage, which the slot retains.
// The slot must have no read in flight, and the caller must not access the
// target until [Ahead.Wait], [Ahead.Settle], or [Ahead.Close] completes it.
// An immediate end-of-file failure leaves the slot idle and returns nil;
// other immediate failures return an error.
func (ahead *Ahead) Start(slot int, block int64, storage, target []byte, offset int64) error {
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

// Wait waits for the slot's read and returns its byte offset, requested length,
// byte count, and error. A short read returns [io.EOF].
// The slot must have a read in flight; completion makes the slot idle.
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

// Settle cancels the slot's read, if one is in flight, and waits until the
// kernel lets go of its storage.
func (ahead *Ahead) Settle(slot int) {
	request := &ahead.requests[slot]
	if !request.pending {
		return
	}
	_ = syscall.CancelIoEx(ahead.handle, &request.overlapped)
	_, _, _, _ = ahead.Wait(slot)
}

// RetainedBytes returns the bytes retained by the slots' storage.
// It returns zero for a nil receiver.
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

// Close settles all reads, releases retained storage, and closes the handle
// and completion events. It may run after the source file is closed.
// Repeated calls are safe.
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
