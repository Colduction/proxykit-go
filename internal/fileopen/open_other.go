//go:build !windows && !linux && !darwin

package fileopen

import "os"

func open(name string, flag int, perm os.FileMode, _ bool) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
