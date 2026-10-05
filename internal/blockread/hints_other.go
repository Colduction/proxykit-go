//go:build !linux && !darwin

package blockread

import "os"

// Hints represents background file-cache advice.
// This platform supports no asynchronous advice for a range.
type Hints struct{}

// Open returns false because this platform has no range-advice implementation.
// Windows reads ahead through an [Ahead] instead.
func (*Hints) Open(*os.File) bool {
	return false
}

// Advise returns false without submitting advice.
func (*Hints) Advise(int64, int) bool {
	return false
}
