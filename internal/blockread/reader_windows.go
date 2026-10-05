//go:build windows

package blockread

import (
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"

	"github.com/colduction/proxykit-go/internal/fileopen"
)

const (
	errorHandleEOF = syscall.Errno(38)
	errorIOPending = syscall.Errno(997)

	fileFlagNoBuffering      = 0x20000000
	fileSkipSetEventOnHandle = 0x2
	fileBasicInfo            = 0
	fileStorageInfo          = 16
	fileAlignmentInfo        = 17
	fileAttributeSparse      = 0x200
	fileAttributeCompressed  = 0x800
	fileAttributeEncrypted   = 0x4000
	directAttributes         = fileAttributeSparse | fileAttributeCompressed | fileAttributeEncrypted
)

type (
	memoryStatus struct {
		length, load                                               uint32
		totalPhysical, availablePhysical, totalPage, availablePage uint64
		totalVirtual, availableVirtual, availableExtendedVirtual   uint64
	}
	basicInformation struct {
		creation, access, write, change int64
		attributes, _                   uint32
	}
	storageInformation struct {
		logicalSector, atomicSector, performanceSector, effectiveAtomicSector uint32
		flags, sectorAlignment, partitionAlignment                            uint32
	}
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW        = kernel32.NewProc("CreateEventW")
	procGetOverlappedResult = kernel32.NewProc("GetOverlappedResult")
	procReOpenFile          = kernel32.NewProc("ReOpenFile")
	procGetFileInfoEx       = kernel32.NewProc("GetFileInformationByHandleEx")
	procGlobalMemoryStatus  = kernel32.NewProc("GlobalMemoryStatusEx")

	forceDirect bool
)

// A Reader performs positional reads of one file through a synchronous handle.
// Initialize it with [Reader.Init] before use.
type Reader struct {
	overlapped syscall.Overlapped
	handle     syscall.Handle
	done       uint32
}

// OpenSource opens the named file for reading with a [Reader].
// The sequential option requests the platform's sequential-access hint.
func OpenSource(name string, sequential bool) (*os.File, error) {
	flag := os.O_RDONLY
	if sequential {
		flag |= fileopen.FlagSequentialScan
	}
	return os.OpenFile(name, flag, 0)
}

// Init binds the reader to an open file, which must remain open while the reader is in use.
func (reader *Reader) Init(file *os.File) {
	_ = fileopen.WithFD(file, func(fd uintptr) error {
		reader.handle = syscall.Handle(fd)
		return nil
	})
}

// ReadAt reads into the buffer at the byte offset as [os.File.ReadAt] does.
// Negative offsets return an error, and short reads return [io.EOF].
func (reader *Reader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, &os.PathError{Op: "readat", Path: "block source", Err: errors.New("negative offset")}
	}
	reader.overlapped = syscall.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(offset >> 32)}
	err := syscall.ReadFile(reader.handle, p, &reader.done, &reader.overlapped)
	return readResult(int(reader.done), len(p), err)
}

// OpenAhead returns an [Ahead] for the reader's file, or nil if its resources
// cannot be opened. The caller must call [Ahead.Close] to release them.
// The direct option bypasses the system cache when the file supports aligned
// reads and its size exceeds available physical memory.
// The sequential option requests a sequential-access hint for buffered reads.
func (reader *Reader) OpenAhead(size int64, sequential, direct bool) *Ahead {
	direct = direct && reader.directReadable(size)
	flags := uintptr(syscall.FILE_FLAG_OVERLAPPED)
	switch {
	case direct:
		flags |= fileFlagNoBuffering
	case sequential:
		flags |= fileopen.FlagSequentialScan
	}
	handle, _, _ := syscall.SyscallN(procReOpenFile.Addr(), uintptr(reader.handle), syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, flags)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		return nil
	}
	_ = syscall.SetFileCompletionNotificationModes(syscall.Handle(handle), fileSkipSetEventOnHandle)
	ahead := &Ahead{handle: syscall.Handle(handle), direct: direct}
	for i := range ahead.requests {
		event, _, _ := syscall.SyscallN(procCreateEventW.Addr(), 0, 1, 0, 0)
		if event == 0 {
			ahead.Close()
			return nil
		}
		ahead.requests[i].event = syscall.Handle(event) | 1
	}
	return ahead
}

func (reader *Reader) directReadable(size int64) bool {
	if !forceDirect {
		memory := memoryStatus{length: uint32(unsafe.Sizeof(memoryStatus{}))}
		if ok, _, _ := syscall.SyscallN(procGlobalMemoryStatus.Addr(), uintptr(unsafe.Pointer(&memory))); ok == 0 ||
			uint64(size) <= memory.availablePhysical {
			return false
		}
	}
	var (
		basic     basicInformation
		storage   storageInformation
		alignment uint32
	)
	return reader.information(fileBasicInfo, unsafe.Pointer(&basic), unsafe.Sizeof(basic)) &&
		basic.attributes&directAttributes == 0 &&
		reader.information(fileStorageInfo, unsafe.Pointer(&storage), unsafe.Sizeof(storage)) &&
		validSector(storage.logicalSector) && validSector(storage.performanceSector) &&
		reader.information(fileAlignmentInfo, unsafe.Pointer(&alignment), unsafe.Sizeof(alignment)) &&
		alignment < PageBytes
}

func (reader *Reader) information(class uintptr, into unsafe.Pointer, size uintptr) bool {
	ok, _, _ := syscall.SyscallN(procGetFileInfoEx.Addr(), uintptr(reader.handle), class, uintptr(into), size)
	return ok != 0
}

func validSector(bytes uint32) bool {
	return bytes != 0 && bytes <= PageBytes && bytes&(bytes-1) == 0
}

// SetForceDirect sets whether [Reader.OpenAhead] bypasses the file-size
// threshold for requested direct reads and returns the prior setting.
// It does not bypass the file's alignment and attribute requirements.
// Calls must be serialized with each other and with [Reader.OpenAhead].
func SetForceDirect(on bool) bool {
	previous := forceDirect
	forceDirect = on
	return previous
}

func readResult(read, length int, err error) (int, error) {
	switch {
	case err == errorHandleEOF:
		return read, io.EOF
	case err != nil:
		return read, &os.PathError{Op: "read", Path: "block source", Err: err}
	case read < length:
		return read, io.EOF
	}
	return read, nil
}
