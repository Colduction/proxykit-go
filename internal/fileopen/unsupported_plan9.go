package fileopen

import "errors"

// Fadvise returns [errors.ErrUnsupported] because Plan 9 has no fadvise call.
func Fadvise(int, int64, int64, int) error {
	return errors.ErrUnsupported
}

// FcntlInt returns [errors.ErrUnsupported] because Plan 9 has no fcntl call.
func FcntlInt(uintptr, int, int) (int, error) {
	return 0, errors.ErrUnsupported
}
