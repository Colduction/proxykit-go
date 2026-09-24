//go:build !windows

package blockread

// An Ahead keeps reads of a file in flight into storage it holds. Only
// Windows provides one; elsewhere [Reader.OpenAhead] returns nil and its
// methods do nothing.
type Ahead struct{}

// Direct reports whether the reads of ahead bypass the system cache.
func (*Ahead) Direct() bool { return false }

// Find returns the slot of the read of block in flight, or -1.
func (*Ahead) Find(int64) int { return -1 }

// Idle returns a slot with no read in flight, or -1.
func (*Ahead) Idle() int { return -1 }

// Block returns the block of the read in slot and whether it is in flight.
func (*Ahead) Block(int) (int64, bool) { return 0, false }

// Swap exchanges the storage slot holds for storage and returns it.
func (*Ahead) Swap(_ int, storage []byte) []byte { return storage }

// Start reads target, which lies in storage, at offset for block in slot.
func (*Ahead) Start(int, int64, []byte, []byte, int64) error { return nil }

// Wait waits for the read in slot and reports its offset, length, count,
// and error.
func (*Ahead) Wait(int) (int64, int, int, error) { return 0, 0, 0, nil }

// Settle cancels the read in slot and waits until it lets go of its storage.
func (*Ahead) Settle(int) {}

// RetainedBytes returns the memory the storage of ahead retains, which is 0
// for a nil Ahead.
func (*Ahead) RetainedBytes() int64 { return 0 }

// Close settles the reads of ahead and releases its resources.
func (*Ahead) Close() {}
