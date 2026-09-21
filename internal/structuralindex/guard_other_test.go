//go:build !linux && !darwin && !windows

package structuralindex_test

import "testing"

func guardedPage(t *testing.T) []byte {
	t.Helper()
	t.Skip("no page protection on this platform")
	return nil
}
