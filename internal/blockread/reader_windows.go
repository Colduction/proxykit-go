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

// A Reader on Windows calls ReadFile on a synchronous handle with the file
// offset in an OVERLAPPED structure, instead of os.File.ReadAt on an
// overlapped handle, whose reads complete through the runtime's I/O
// completion port. On an uncached file, reads of 1 MiB and 4 MiB this way
// measured 1.5 to 1.7 times the throughput of ReadAt, since the cache manager
// reads ahead of a synchronous reader, and slightly more on a cached one.

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

// A Reader performs positional reads of one file. Init binds it to the file.
type Reader struct {
	overlapped syscall.Overlapped
	handle     syscall.Handle
	done       uint32
}

// OpenSource opens name for reading with a [Reader], with the platform's
// hint of sequential reading when sequential is true.
func OpenSource(name string, sequential bool) (*os.File, error) {
	flag := os.O_RDONLY
	if sequential {
		flag |= fileopen.FlagSequentialScan
	}
	return os.OpenFile(name, flag, 0)
}

// Init binds reader to file, which must stay open while reader is in use.
func (reader *Reader) Init(file *os.File) {
	// File.Fd would also detach the handle from the runtime's completion
	// port, which a synchronous handle never joins.
	_ = fileopen.WithFD(file, func(fd uintptr) error {
		reader.handle = syscall.Handle(fd)
		return nil
	})
}

// ReadAt reads len(p) bytes at offset as [os.File.ReadAt] does.
func (reader *Reader) ReadAt(p []byte, offset int64) (int, error) {
	// Windows interprets offset -2 as the current file position rather than
	// rejecting it. Positional reads never accept a negative offset.
	if offset < 0 {
		return 0, &os.PathError{Op: "readat", Path: "block source", Err: errors.New("negative offset")}
	}
	reader.overlapped = syscall.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(offset >> 32)}
	err := syscall.ReadFile(reader.handle, p, &reader.done, &reader.overlapped)
	return readResult(int(reader.done), len(p), err)
}

// OpenAhead returns an [Ahead] for the file of reader, or nil where the
// platform offers none, which is every platform but Windows. The Ahead reads
// without buffering when direct is true, the caller's reads suit that, and the
// file is larger than the memory available to cache it; sequential passes the
// hint of sequential reading to a buffered one. The caller closes it.
func (reader *Reader) OpenAhead(size int64, sequential, direct bool) *Ahead {
	// ReOpenFile opens the same file again for overlapped reads, which the
	// runtime's completion port never sees. Each read signals a manual-reset
	// event; setting its low bit in the OVERLAPPED structure keeps the
	// completion off any completion port as well.
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
	// The events signal completion, so the handle need not be signaled too.
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
	// It reports whether the file suits reads without buffering: the cache
	// could not hold it, and the file system takes sector-aligned reads of
	// it. memoryStatus, basicInformation, and storageInformation have the
	// layouts of MEMORYSTATUSEX, FILE_BASIC_INFO, and FILE_STORAGE_INFO,
	// whose fields give them the alignment the calls require.
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

// SetForceDirect makes [Reader.OpenAhead] read without buffering whatever
// the size of the file, when the caller asks for it, and returns the
// previous setting. Tests use it.
func SetForceDirect(on bool) bool {
	previous := forceDirect
	forceDirect = on
	return previous
}

func readResult(read, length int, err error) (int, error) {
	// It reports a read the way ReadAt does: a count short of length ends
	// at the end of the file.
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
