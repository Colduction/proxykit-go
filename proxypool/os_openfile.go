//go:build !windows && !linux && !darwin

package proxypool

import "os"

// OpenFile opens name using flag and perm, with platform-specific access
// behavior selected by mode.
func OpenFile(name string, flag int, perm os.FileMode, _ Mode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
