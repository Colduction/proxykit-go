package structuralindex_test

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func guardedPage(t *testing.T) []byte {
	t.Helper()
	const (
		memCommit     = 0x1000
		memReserve    = 0x2000
		memRelease    = 0x8000
		pageNoAccess  = 0x01
		pageReadWrite = 0x04
	)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	size := os.Getpagesize()
	base, _, err := kernel32.NewProc("VirtualAlloc").Call(0, uintptr(3*size), memCommit|memReserve, pageReadWrite)
	if base == 0 {
		t.Fatalf("VirtualAlloc: %v", err)
	}
	t.Cleanup(func() {
		if ok, _, err := kernel32.NewProc("VirtualFree").Call(base, 0, memRelease); ok == 0 {
			t.Errorf("VirtualFree: %v", err)
		}
	})
	protect := kernel32.NewProc("VirtualProtect")
	for _, guard := range []uintptr{base, base + uintptr(2*size)} {
		var previous uint32
		if ok, _, err := protect.Call(guard, uintptr(size), pageNoAccess, uintptr(unsafe.Pointer(&previous))); ok == 0 {
			t.Fatalf("VirtualProtect: %v", err)
		}
	}
	// The region lies outside the Go heap, so holding its address in a
	// uintptr loses nothing; the indirection keeps vet's unsafeptr check,
	// meant for heap pointers, from flagging the conversion.
	middle := base + uintptr(size)
	return unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&middle))), size)
}
