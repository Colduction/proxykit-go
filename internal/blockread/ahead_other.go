//go:build !windows

package blockread

// An Ahead represents reads in flight into retained storage.
// This platform provides no implementation: [Reader.OpenAhead] returns nil,
// and the methods return their unavailable results without accessing the receiver.
type Ahead struct{}

// Direct reports whether reads bypass the system cache; it returns false.
func (*Ahead) Direct() bool { return false }

// Find returns -1 because this platform has no reads in flight.
func (*Ahead) Find(int64) int { return -1 }

// Idle returns -1 because this platform has no read slots.
func (*Ahead) Idle() int { return -1 }

// Block returns zero and false because this platform has no reads in flight.
func (*Ahead) Block(int) (int64, bool) { return 0, false }

// Swap returns the supplied storage without retaining it.
func (*Ahead) Swap(_ int, storage []byte) []byte { return storage }

// Start returns nil without submitting a read.
func (*Ahead) Start(int, int64, []byte, []byte, int64) error { return nil }

// Wait returns zero offsets and counts and a nil error without waiting.
func (*Ahead) Wait(int) (int64, int, int, error) { return 0, 0, 0, nil }

// Settle returns without performing an operation.
func (*Ahead) Settle(int) {}

// RetainedBytes returns zero because this platform retains no storage.
func (*Ahead) RetainedBytes() int64 { return 0 }

// Close returns without performing an operation.
func (*Ahead) Close() {}
