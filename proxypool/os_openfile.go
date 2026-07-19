//go:build !windows && !linux && !darwin

package proxypool

import "os"

func OpenFile(name string, flag int, perm os.FileMode, _ Mode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
