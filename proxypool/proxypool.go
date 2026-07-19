// Package proxypool provides bounded-memory iteration over newline-delimited
// proxy files.
package proxypool

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"sync"
)

const (
	// DefaultSequentialBufferBytes is the default sequential read-buffer target.
	// Small files use a smaller buffer.
	DefaultSequentialBufferBytes = 1 << 20

	// DefaultBlockBytes is the default shuffled locality-block size.
	DefaultBlockBytes = 4 << 20

	// DefaultRegionBytes is the default shuffled locality-region size.
	DefaultRegionBytes int64 = 1 << 30

	// DefaultMaxLineBytes is the default maximum proxy size, excluding CRLF or LF.
	DefaultMaxLineBytes = 64 << 10

	tailReadBytes = 4 << 10
	mixIncrement  = 0x9e3779b97f4a7c15
)

var (
	// ErrClosed reports use of a closed pool.
	ErrClosed = errors.New("proxypool: pool closed")

	// ErrSourceChanged reports that source size or modification time changed.
	ErrSourceChanged = errors.New("proxypool: source file changed")

	// ErrLineTooLong reports a line larger than [Options.MaxLineBytes].
	ErrLineTooLong = errors.New("proxypool: line too long")
)

// Mode specifies iteration order.
type Mode uint8

const (
	// ModeSequential streams lines in physical file order.
	ModeSequential Mode = iota

	// ModeShuffled permutes regions and lines while preserving block locality.
	// It needs no sidecar index and opens in constant time.
	ModeShuffled
	modeCount
)

// IsValid reports whether mode is supported.
func (mode Mode) IsValid() bool {
	return mode < modeCount
}

// Valid returns an error when mode is unsupported.
func (mode Mode) Valid() error {
	if mode.IsValid() {
		return nil
	}
	return fmt.Errorf("proxypool: invalid mode: %d", mode)
}

// Options configures [Open]. Its zero value selects one non-repeating
// sequential cursor with bounded defaults.
type Options struct {
	// SequentialBufferBytes sets sequential read-buffer capacity. Values below
	// one use [DefaultSequentialBufferBytes].
	SequentialBufferBytes int `json:"sequentialBufferBytes,omitempty" yaml:"sequentialBufferBytes,omitempty" xml:"sequentialBufferBytes,omitempty" cbor:"sequentialBufferBytes,omitempty" bson:"sequentialBufferBytes,omitempty" msgpack:"sequentialBufferBytes,omitempty" toml:"sequentialBufferBytes,omitempty" mapstructure:"sequentialBufferBytes,omitempty"`

	// BlockBytes sets shuffled read granularity and retained data-buffer size.
	// Values below one use [DefaultBlockBytes]. Keep BlockBytes at least
	// MaxLineBytes+2 to avoid repeated bounded scans of continuation-only blocks.
	BlockBytes int `json:"blockBytes,omitempty" yaml:"blockBytes,omitempty" xml:"blockBytes,omitempty" cbor:"blockBytes,omitempty" bson:"blockBytes,omitempty" msgpack:"blockBytes,omitempty" toml:"blockBytes,omitempty" mapstructure:"blockBytes,omitempty"`

	// RegionBytes sets shuffled seek locality. Values below one use
	// [DefaultRegionBytes]. Effective size is a whole number of blocks. Larger
	// regions reduce seeks; smaller regions produce finer global mixing.
	RegionBytes int64 `json:"regionBytes,omitempty" yaml:"regionBytes,omitempty" xml:"regionBytes,omitempty" cbor:"regionBytes,omitempty" bson:"regionBytes,omitempty" msgpack:"regionBytes,omitempty" toml:"regionBytes,omitempty" mapstructure:"regionBytes,omitempty"`

	// MaxLineBytes bounds one proxy, excluding one LF and optional preceding CR.
	// Values below one use [DefaultMaxLineBytes]. This bound is required because
	// no line-returning API can promise bounded memory for an unbounded line.
	MaxLineBytes int `json:"maxLineBytes,omitempty" yaml:"maxLineBytes,omitempty" xml:"maxLineBytes,omitempty" cbor:"maxLineBytes,omitempty" bson:"maxLineBytes,omitempty" msgpack:"maxLineBytes,omitempty" toml:"maxLineBytes,omitempty" mapstructure:"maxLineBytes,omitempty"`

	// Seed selects deterministic shuffled order for the same source, options,
	// and package version. Zero selects a random seed; retrieve the selected
	// value from [Pool.Stats].
	Seed uint64 `json:"seed,omitempty" yaml:"seed,omitempty" xml:"seed,omitempty" cbor:"seed,omitempty" bson:"seed,omitempty" msgpack:"seed,omitempty" toml:"seed,omitempty" mapstructure:"seed,omitempty"`

	// ShardCount partitions permuted regions among independent pools. Values
	// below one select one shard. Pools using the same source, BlockBytes,
	// RegionBytes, nonzero Seed, and ShardCount, with one pool for every
	// ShardIndex in [0, ShardCount), collectively return every line exactly once.
	ShardCount int `json:"shardCount,omitempty" yaml:"shardCount,omitempty" xml:"shardCount,omitempty" cbor:"shardCount,omitempty" bson:"shardCount,omitempty" msgpack:"shardCount,omitempty" toml:"shardCount,omitempty" mapstructure:"shardCount,omitempty"`

	// ShardIndex selects this pool's zero-based shard.
	ShardIndex int `json:"shardIndex,omitempty" yaml:"shardIndex,omitempty" xml:"shardIndex,omitempty" cbor:"shardIndex,omitempty" bson:"shardIndex,omitempty" msgpack:"shardIndex,omitempty" toml:"shardIndex,omitempty" mapstructure:"shardIndex,omitempty"`

	// Mode controls iteration order.
	Mode Mode `json:"mode,omitempty" yaml:"mode,omitempty" xml:"mode,omitempty" cbor:"mode,omitempty" bson:"mode,omitempty" msgpack:"mode,omitempty" toml:"mode,omitempty" mapstructure:"mode,omitempty"`

	// Reuse starts another cycle after exhaustion. Shuffled cycles use new
	// permutations. Reuse cannot be combined with sharding because a valid shard
	// cycle may be empty.
	Reuse bool `json:"reuse,omitempty" yaml:"reuse,omitempty" xml:"reuse,omitempty" cbor:"reuse,omitempty" bson:"reuse,omitempty" msgpack:"reuse,omitempty" toml:"reuse,omitempty" mapstructure:"reuse,omitempty"`
}

// Stats is a point-in-time pool snapshot. File-size-derived counts use int64,
// so 10 TiB files remain representable on 32-bit and 64-bit platforms.
type Stats struct {
	// FileSize is source size at open time, in bytes.
	FileSize int64 `json:"fileSize" yaml:"fileSize" xml:"fileSize" cbor:"fileSize" bson:"fileSize" msgpack:"fileSize" toml:"fileSize" mapstructure:"fileSize"`

	// Blocks is total physical block count. It is zero in sequential mode.
	Blocks int64 `json:"blocks" yaml:"blocks" xml:"blocks" cbor:"blocks" bson:"blocks" msgpack:"blocks" toml:"blocks" mapstructure:"blocks"`

	// Regions is total locality-region count before sharding. It is zero in
	// sequential mode.
	Regions int64 `json:"regions" yaml:"regions" xml:"regions" cbor:"regions" bson:"regions" msgpack:"regions" toml:"regions" mapstructure:"regions"`

	// ShardRegions is number of regions assigned to this shuffled pool.
	ShardRegions int64 `json:"shardRegions" yaml:"shardRegions" xml:"shardRegions" cbor:"shardRegions" bson:"shardRegions" msgpack:"shardRegions" toml:"shardRegions" mapstructure:"shardRegions"`

	// Cursor is number of lines returned since open or [Pool.Reset].
	Cursor int64 `json:"cursor" yaml:"cursor" xml:"cursor" cbor:"cursor" bson:"cursor" msgpack:"cursor" toml:"cursor" mapstructure:"cursor"`

	// Cycle is zero-based automatic reuse cycle.
	Cycle uint64 `json:"cycle" yaml:"cycle" xml:"cycle" cbor:"cycle" bson:"cycle" msgpack:"cycle" toml:"cycle" mapstructure:"cycle"`

	// Seed is actual shuffle seed, including an automatically selected seed. It
	// is zero in sequential mode.
	Seed uint64 `json:"seed" yaml:"seed" xml:"seed" cbor:"seed" bson:"seed" msgpack:"seed" toml:"seed" mapstructure:"seed"`

	// RetainedBytes is current Go buffer capacity, excluding small fixed state.
	RetainedBytes int64 `json:"retainedBytes" yaml:"retainedBytes" xml:"retainedBytes" cbor:"retainedBytes" bson:"retainedBytes" msgpack:"retainedBytes" toml:"retainedBytes" mapstructure:"retainedBytes"`

	// MaxRetainedBytes is the configured upper bound for retained data, line,
	// and offset buffers, excluding caller-owned results and small fixed state.
	MaxRetainedBytes int64 `json:"maxRetainedBytes" yaml:"maxRetainedBytes" xml:"maxRetainedBytes" cbor:"maxRetainedBytes" bson:"maxRetainedBytes" msgpack:"maxRetainedBytes" toml:"maxRetainedBytes" mapstructure:"maxRetainedBytes"`

	// SequentialBufferBytes is effective sequential buffer capacity.
	SequentialBufferBytes int `json:"sequentialBufferBytes" yaml:"sequentialBufferBytes" xml:"sequentialBufferBytes" cbor:"sequentialBufferBytes" bson:"sequentialBufferBytes" msgpack:"sequentialBufferBytes" toml:"sequentialBufferBytes" mapstructure:"sequentialBufferBytes"`

	// BlockBytes is effective shuffled block size. It is zero in sequential mode.
	BlockBytes int `json:"blockBytes" yaml:"blockBytes" xml:"blockBytes" cbor:"blockBytes" bson:"blockBytes" msgpack:"blockBytes" toml:"blockBytes" mapstructure:"blockBytes"`

	// RegionBytes is effective shuffled locality-region size. It is zero in
	// sequential mode.
	RegionBytes int64 `json:"regionBytes" yaml:"regionBytes" xml:"regionBytes" cbor:"regionBytes" bson:"regionBytes" msgpack:"regionBytes" toml:"regionBytes" mapstructure:"regionBytes"`

	// MaxLineBytes is maximum returned line size.
	MaxLineBytes int `json:"maxLineBytes" yaml:"maxLineBytes" xml:"maxLineBytes" cbor:"maxLineBytes" bson:"maxLineBytes" msgpack:"maxLineBytes" toml:"maxLineBytes" mapstructure:"maxLineBytes"`

	// ShardCount is total configured shards.
	ShardCount int `json:"shardCount" yaml:"shardCount" xml:"shardCount" cbor:"shardCount" bson:"shardCount" msgpack:"shardCount" toml:"shardCount" mapstructure:"shardCount"`

	// ShardIndex identifies this pool's shard.
	ShardIndex int `json:"shardIndex" yaml:"shardIndex" xml:"shardIndex" cbor:"shardIndex" bson:"shardIndex" msgpack:"shardIndex" toml:"shardIndex" mapstructure:"shardIndex"`

	// Mode is configured iteration order.
	Mode Mode `json:"mode" yaml:"mode" xml:"mode" cbor:"mode" bson:"mode" msgpack:"mode" toml:"mode" mapstructure:"mode"`

	// Reuse reports whether automatic cycles are enabled.
	Reuse bool `json:"reuse" yaml:"reuse" xml:"reuse" cbor:"reuse" bson:"reuse" msgpack:"reuse" toml:"reuse" mapstructure:"reuse"`

	// Closed reports whether the pool is closed. Nil and zero pools report true.
	Closed bool `json:"closed" yaml:"closed" xml:"closed" cbor:"closed" bson:"closed" msgpack:"closed" toml:"closed" mapstructure:"closed"`
}

// Pool reads one immutable regular file. Its zero value behaves as closed. A
// Pool must not be copied after first use. A Pool is safe for concurrent use,
// though calls share one cursor and therefore serialize. File I/O occurs while
// holding that cursor lock, so Close, Reset, and other reads wait for an active
// read. For parallel storage reads, open explicitly partitioned pools with
// [Options.ShardCount].
//
// Shuffled mode keeps one data block and uint32 line offsets. Retained memory
// is bounded by roughly BlockBytes + MaxLineBytes + 4*BlockBytes. It does not
// preload source data, memory-map the file, build a sidecar, or start workers.
type Pool struct {
	noCopy           noCopy
	terminal         error
	file             *os.File
	section          *io.SectionReader
	reader           *bufio.Reader
	buffer           []byte
	offsets          []uint32
	seqLine          []byte
	fileSize         int64
	modified         int64
	blockCount       int64
	regionCount      int64
	regionsForShard  int64
	blocksPerRegion  int64
	regionCursor     int64
	nextRegion       int64
	regionStep       int64
	regionAdvance    int64
	regionBlockBase  int64
	regionBlocks     int64
	regionBlockNext  int64
	regionRotation   int64
	lineCursor       int64
	nextLine         int64
	lineStep         int64
	cursor           int64
	cycleStartCursor int64
	seqOffset        int64
	regionBytes      int64
	cycle            uint64
	seed             uint64
	cycleSeed        uint64
	sequentialBytes  int
	blockBytes       int
	maxLineBytes     int
	shardCount       int
	shardIndex       int
	mu               sync.Mutex
	mode             Mode
	reuse            bool
	closed           bool
}

// noCopy makes go vet report copies of a Pool after first use.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// New opens path with mode and reuse. Use [Open] for memory, locality, seed,
// or shard controls.
func New(path string, mode Mode, reuse bool) (*Pool, error) {
	return Open(path, Options{Mode: mode, Reuse: reuse})
}

// Open opens an immutable newline-delimited regular file. Open performs no
// source scan, proportional allocation, sidecar construction, or preloading.
// The source must not change until [Pool.Close]. Size and modification-time
// checks detect ordinary changes at bounded checkpoints, not adversarial
// same-metadata rewrites.
func Open(path string, options Options) (*Pool, error) {
	err := options.Mode.Valid()
	if err != nil {
		return nil, err
	}
	file, err := OpenFile(path, os.O_RDONLY, 0, options.Mode)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("proxypool: %q is not a regular file", path)
	}
	sequentialBytes, maxLineBytes, shardCount :=
		positiveOrDefault(options.SequentialBufferBytes, DefaultSequentialBufferBytes),
		positiveOrDefault(options.MaxLineBytes, DefaultMaxLineBytes),
		max(options.ShardCount, 1)
	if options.ShardIndex < 0 || options.ShardIndex >= shardCount {
		file.Close()
		return nil, fmt.Errorf("proxypool: shard index %d outside [0,%d)", options.ShardIndex, shardCount)
	}
	if options.Mode == ModeSequential && (shardCount != 1 || options.ShardIndex != 0) {
		file.Close()
		return nil, errors.New("proxypool: sequential mode does not support sharding")
	}
	if options.Reuse && shardCount > 1 {
		file.Close()
		return nil, errors.New("proxypool: reuse cannot be combined with sharding")
	}
	maxInt := int(^uint(0) >> 1)
	if maxLineBytes > maxInt-2 {
		file.Close()
		return nil, errors.New("proxypool: maximum line size overflows int")
	}
	if options.Mode == ModeSequential {
		sequentialBytes = boundedSequentialBuffer(sequentialBytes, info.Size())
		if uint64(sequentialBytes) > uint64(^uint64(0)>>1)-uint64(maxLineBytes)-2 {
			file.Close()
			return nil, errors.New("proxypool: sequential memory bound overflows int64")
		}
		section := io.NewSectionReader(file, 0, info.Size())
		return &Pool{
			file:            file,
			section:         section,
			reader:          bufio.NewReaderSize(section, sequentialBytes),
			fileSize:        info.Size(),
			modified:        info.ModTime().UnixNano(),
			sequentialBytes: sequentialBytes,
			maxLineBytes:    maxLineBytes,
			shardCount:      1,
			mode:            ModeSequential,
			reuse:           options.Reuse,
		}, nil
	}
	blockBytes := positiveOrDefault(options.BlockBytes, DefaultBlockBytes)
	regionBytes := options.RegionBytes
	if regionBytes < 1 {
		regionBytes = DefaultRegionBytes
	}
	if blockBytes > maxInt-maxLineBytes-3 {
		file.Close()
		return nil, errors.New("proxypool: block and maximum line sizes overflow int")
	}
	if uint64(blockBytes+maxLineBytes+3) > uint64(^uint32(0)) {
		file.Close()
		return nil, errors.New("proxypool: block and maximum line sizes exceed uint32 offsets")
	}
	if regionBytes < int64(blockBytes) {
		file.Close()
		return nil, fmt.Errorf("proxypool: region size %d is smaller than block size %d", regionBytes, blockBytes)
	}
	blocksPerRegion := regionBytes / int64(blockBytes)
	if blocksPerRegion > int64(^uint64(0)>>1)/int64(blockBytes) {
		file.Close()
		return nil, errors.New("proxypool: effective region size overflows int64")
	}
	regionBytes = blocksPerRegion * int64(blockBytes)
	seed := options.Seed
	if seed == 0 {
		seed = rand.Uint64()
		if seed == 0 {
			seed = mixIncrement
		}
	}
	pool := &Pool{
		file:         file,
		fileSize:     info.Size(),
		modified:     info.ModTime().UnixNano(),
		regionBytes:  regionBytes,
		seed:         seed,
		blockBytes:   blockBytes,
		maxLineBytes: maxLineBytes,
		shardCount:   shardCount,
		shardIndex:   options.ShardIndex,
		mode:         options.Mode,
		reuse:        options.Reuse,
	}
	pool.blockCount = ceilingQuotient(info.Size(), int64(blockBytes))
	pool.blocksPerRegion = blocksPerRegion
	pool.regionCount = ceilingQuotient(pool.blockCount, blocksPerRegion)
	pool.regionsForShard = shardItemCount(pool.regionCount, int64(shardCount), int64(options.ShardIndex))
	pool.startCycleLocked(0)
	return pool, nil
}

// Next returns next proxy. It returns [io.EOF] after exhaustion, [ErrClosed]
// after close, and the underlying I/O or validation error on failure. A
// non-EOF failure remains terminal until a successful [Pool.Reset].
func (pool *Pool) Next() (string, error) {
	if pool == nil {
		return "", ErrClosed
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	line, err := pool.nextLocked()
	if err != nil {
		return "", err
	}
	return string(line), nil
}

// NextBytes appends next proxy to dst[:0]. Returned bytes belong to caller.
// With sufficient dst capacity, steady-state calls allocate no memory after
// internal buffers reach the required capacities. It returns the same errors as
// [Pool.Next] and returns dst[:0] on error.
func (pool *Pool) NextBytes(dst []byte) ([]byte, error) {
	if pool == nil {
		return dst[:0], ErrClosed
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	line, err := pool.nextLocked()
	if err != nil {
		return dst[:0], err
	}
	return append(dst[:0], line...), nil
}

func (pool *Pool) nextLocked() ([]byte, error) {
	if pool.closed || pool.file == nil {
		return nil, ErrClosed
	}
	if pool.terminal != nil {
		return nil, pool.terminal
	}
	if pool.mode == ModeSequential {
		line, err := pool.nextSequentialLocked()
		if err != nil && !errors.Is(err, io.EOF) {
			pool.terminal = err
		}
		return line, err
	}
	line, err := pool.nextShuffledLocked()
	if err != nil && !errors.Is(err, io.EOF) {
		pool.terminal = err
	}
	return line, err
}

func (pool *Pool) nextSequentialLocked() ([]byte, error) {
	pool.seqLine = pool.seqLine[:0]
	lineOffset := pool.seqOffset
	for {
		fragment, err := pool.reader.ReadSlice('\n')
		pool.seqOffset += int64(len(fragment))
		if errors.Is(err, io.EOF) && pool.seqOffset < pool.fileSize {
			if changedErr := pool.validateSourceLocked(); changedErr != nil {
				return nil, changedErr
			}
			return nil, io.ErrUnexpectedEOF
		}
		if len(pool.seqLine) == 0 && (err == nil || errors.Is(err, io.EOF)) {
			if len(fragment) > 0 {
				line := trimLineEnding(fragment)
				if len(line) > pool.maxLineBytes {
					return nil, pool.lineTooLong(lineOffset)
				}
				pool.cursor++
				return line, nil
			}
		} else if len(fragment) > 0 {
			if !pool.appendSequentialFragment(fragment) {
				return nil, pool.lineTooLong(lineOffset)
			}
		}
		switch {
		case err == nil:
			line := trimLineEnding(pool.seqLine)
			if len(line) > pool.maxLineBytes {
				return nil, pool.lineTooLong(lineOffset)
			}
			pool.cursor++
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case !errors.Is(err, io.EOF):
			return nil, err
		case len(pool.seqLine) > 0:
			line := trimLineEnding(pool.seqLine)
			if len(line) > pool.maxLineBytes {
				return nil, pool.lineTooLong(lineOffset)
			}
			pool.cursor++
			return line, nil
		case !pool.reuse || pool.fileSize == 0:
			if err = pool.validateSourceLocked(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		if err = pool.validateSourceLocked(); err != nil {
			return nil, err
		}
		if _, err = pool.section.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		pool.reader.Reset(pool.section)
		pool.seqOffset = 0
		pool.cycle++
		lineOffset = 0
	}
}

func (pool *Pool) appendSequentialFragment(fragment []byte) bool {
	limit := pool.maxLineBytes + 2
	if len(fragment) > limit-len(pool.seqLine) {
		return false
	}
	length := len(pool.seqLine) + len(fragment)
	if cap(pool.seqLine) < length {
		capacity := max(length, cap(pool.seqLine)*2)
		if capacity > limit || capacity < cap(pool.seqLine) {
			capacity = limit
		}
		grown := make([]byte, len(pool.seqLine), capacity)
		copy(grown, pool.seqLine)
		pool.seqLine = grown
	}
	pool.seqLine = append(pool.seqLine, fragment...)
	return true
}

func (pool *Pool) nextShuffledLocked() ([]byte, error) {
	for pool.lineCursor >= int64(len(pool.offsets)-1) {
		if err := pool.loadNextBlockLocked(); err != nil {
			return nil, err
		}
	}
	start, end := pool.offsets[pool.nextLine], pool.offsets[pool.nextLine+1]
	pool.lineCursor++
	pool.nextLine += pool.lineStep
	if pool.nextLine >= int64(len(pool.offsets)-1) {
		pool.nextLine -= int64(len(pool.offsets) - 1)
	}
	pool.cursor++
	return trimLineEnding(pool.buffer[start:end]), nil
}

func (pool *Pool) loadNextBlockLocked() error {
	for {
		if pool.regionBlockNext < pool.regionBlocks {
			block := pool.regionBlockBase + int64(addModulo(
				uint64(pool.regionRotation),
				uint64(pool.regionBlockNext),
				uint64(pool.regionBlocks),
			))
			pool.regionBlockNext++
			if err := pool.loadBlockLocked(block); err != nil {
				return err
			}
			if len(pool.offsets) > 1 {
				return nil
			}
			continue
		}
		if pool.regionCursor == pool.regionsForShard {
			if err := pool.validateSourceLocked(); err != nil {
				return err
			}
			if !pool.reuse || pool.fileSize == 0 || pool.cursor == pool.cycleStartCursor {
				return io.EOF
			}
			pool.startCycleLocked(pool.cycle + 1)
			continue
		}
		if err := pool.validateSourceLocked(); err != nil {
			return err
		}
		region := pool.nextRegion
		pool.regionCursor++
		pool.nextRegion = int64(addModulo(uint64(pool.nextRegion), uint64(pool.regionAdvance), uint64(pool.regionCount)))
		pool.regionBlockBase = region * pool.blocksPerRegion
		pool.regionBlocks = min(pool.blocksPerRegion, pool.blockCount-pool.regionBlockBase)
		pool.regionBlockNext = 0
		pool.regionRotation = int64(mix64(pool.cycleSeed^uint64(region)) % uint64(pool.regionBlocks))
	}
}

func (pool *Pool) loadBlockLocked(block int64) error {
	blockStart := block * int64(pool.blockBytes)
	baseBytes := int(min(int64(pool.blockBytes), pool.fileSize-blockStart))
	var prefix int
	readOffset := blockStart
	if blockStart > 0 {
		prefix = 1
		readOffset--
	}
	ownershipEnd := prefix + baseBytes
	lookaheadBytes := min(tailReadBytes, pool.maxLineBytes+2)
	if remaining := pool.fileSize - blockStart - int64(baseBytes); int64(lookaheadBytes) > remaining {
		lookaheadBytes = int(remaining)
	}
	readBytes := ownershipEnd + lookaheadBytes
	pool.resizeBuffer(readBytes)
	read, err := pool.file.ReadAt(pool.buffer, readOffset)
	if read != readBytes {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("proxypool: read block at %d: %w", blockStart, err)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("proxypool: read block at %d: %w", blockStart, err)
	}
	pool.offsets = pool.offsets[:0]
	lineStart := prefix
	if prefix == 1 && pool.buffer[0] != '\n' {
		newline := bytes.IndexByte(pool.buffer[prefix:ownershipEnd], '\n')
		if newline < 0 {
			continuationBytes := baseBytes
			if ownershipEnd < len(pool.buffer) && pool.buffer[ownershipEnd] == '\n' && pool.buffer[ownershipEnd-1] == '\r' {
				continuationBytes--
			}
			if err = pool.validateContinuationLocked(blockStart, continuationBytes); err != nil {
				return err
			}
			pool.lineCursor, pool.nextLine = 0, 0
			return nil
		}
		lineStart = prefix + newline + 1
	}
	if lineStart >= ownershipEnd {
		pool.lineCursor, pool.nextLine = 0, 0
		return nil
	}
	pool.appendOffset(uint32(lineStart))
	for search := lineStart; search < ownershipEnd; {
		newline := bytes.IndexByte(pool.buffer[search:ownershipEnd], '\n')
		if newline < 0 {
			break
		}
		lineEnd := search + newline + 1
		if exceedsLineLimit(pool.buffer[lineStart:lineEnd], pool.maxLineBytes) {
			return pool.lineTooLong(blockStart + int64(lineStart-prefix))
		}
		pool.appendOffset(uint32(lineEnd))
		lineStart, search = lineEnd, lineEnd
	}
	if lineStart < ownershipEnd {
		if blockStart+int64(baseBytes) < pool.fileSize {
			lineEnd, err := pool.extendLineLocked(readOffset, lineStart, ownershipEnd)
			if err != nil {
				return err
			}
			pool.appendOffset(uint32(lineEnd))
		} else {
			pool.buffer = pool.buffer[:ownershipEnd]
			if exceedsLineLimit(pool.buffer[lineStart:ownershipEnd], pool.maxLineBytes) {
				return pool.lineTooLong(blockStart + int64(lineStart-prefix))
			}
			pool.appendOffset(uint32(ownershipEnd))
		}
	}
	lines := int64(len(pool.offsets) - 1)
	pool.lineStep, pool.nextLine = permutationParams(lines, mix64(pool.cycleSeed^uint64(block)))
	pool.lineCursor = 0
	return nil
}

func (pool *Pool) extendLineLocked(readOffset int64, lineStart, searchStart int) (int, error) {
	limit := lineStart + pool.maxLineBytes + 2
	if newline := bytes.IndexByte(pool.buffer[searchStart:], '\n'); newline >= 0 {
		lineEnd := searchStart + newline + 1
		pool.buffer = pool.buffer[:lineEnd]
		if exceedsLineLimit(pool.buffer[lineStart:lineEnd], pool.maxLineBytes) {
			return 0, pool.lineTooLong(readOffset + int64(lineStart))
		}
		return lineEnd, nil
	}
	for len(pool.buffer) < limit {
		absoluteEnd := readOffset + int64(len(pool.buffer))
		if absoluteEnd == pool.fileSize {
			if exceedsLineLimit(pool.buffer[lineStart:], pool.maxLineBytes) {
				return 0, pool.lineTooLong(readOffset + int64(lineStart))
			}
			return len(pool.buffer), nil
		}
		readBytes := min(tailReadBytes, limit-len(pool.buffer))
		if remaining := pool.fileSize - absoluteEnd; int64(readBytes) > remaining {
			readBytes = int(remaining)
		}
		oldLength := len(pool.buffer)
		pool.resizeBuffer(oldLength + readBytes)
		read, err := pool.file.ReadAt(pool.buffer[oldLength:], absoluteEnd)
		pool.buffer = pool.buffer[:oldLength+read]
		if newline := bytes.IndexByte(pool.buffer[oldLength:], '\n'); newline >= 0 {
			lineEnd := oldLength + newline + 1
			pool.buffer = pool.buffer[:lineEnd]
			if exceedsLineLimit(pool.buffer[lineStart:lineEnd], pool.maxLineBytes) {
				return 0, pool.lineTooLong(readOffset + int64(lineStart))
			}
			return lineEnd, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("proxypool: extend line at %d: %w", readOffset+int64(lineStart), err)
		}
		if read != readBytes {
			if readOffset+int64(len(pool.buffer)) == pool.fileSize {
				continue
			}
			return 0, fmt.Errorf("proxypool: extend line at %d: %w", readOffset+int64(lineStart), io.ErrUnexpectedEOF)
		}
	}
	return 0, pool.lineTooLong(readOffset + int64(lineStart))
}

func (pool *Pool) resizeBuffer(length int) {
	if cap(pool.buffer) >= length {
		pool.buffer = pool.buffer[:length]
		return
	}
	maximum := pool.blockBytes + pool.maxLineBytes + 3
	capacity := length
	if cap(pool.buffer) == 0 && pool.fileSize > int64(pool.blockBytes) {
		capacity = max(capacity, min(maximum, pool.blockBytes+1+min(tailReadBytes, pool.maxLineBytes+2)))
	} else if cap(pool.buffer) > 0 {
		base := min(pool.blockBytes+1, maximum)
		tailCapacity := max(0, cap(pool.buffer)-base)
		nextTailCapacity, maximumTailCapacity := tailCapacity*2, maximum-base
		if nextTailCapacity < tailReadBytes {
			nextTailCapacity = tailReadBytes
		}
		if nextTailCapacity < tailCapacity || nextTailCapacity > maximumTailCapacity {
			nextTailCapacity = maximumTailCapacity
		}
		capacity = max(length, base+nextTailCapacity)
	}
	if capacity > maximum || capacity < cap(pool.buffer) {
		capacity = maximum
	}
	grown := make([]byte, len(pool.buffer), capacity)
	copy(grown, pool.buffer)
	pool.buffer = grown[:length]
}

func (pool *Pool) validateContinuationLocked(blockStart int64, contentBytes int) error {
	if contentBytes >= pool.maxLineBytes {
		return pool.lineTooLong(blockStart - 1)
	}
	remaining := pool.maxLineBytes - contentBytes + 1
	position := blockStart
	scratch := pool.buffer[:min(tailReadBytes, cap(pool.buffer))]
	for scanned := 0; scanned < remaining && position > 0; {
		readBytes := min(len(scratch), remaining-scanned)
		if int64(readBytes) > position {
			readBytes = int(position)
		}
		readOffset := position - int64(readBytes)
		read, err := pool.file.ReadAt(scratch[:readBytes], readOffset)
		if read != readBytes {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("proxypool: inspect line before %d: %w", blockStart, err)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("proxypool: inspect line before %d: %w", blockStart, err)
		}
		if newline := bytes.LastIndexByte(scratch[:read], '\n'); newline >= 0 {
			if scanned+read-newline-1+contentBytes > pool.maxLineBytes {
				return pool.lineTooLong(readOffset + int64(newline+1))
			}
			return nil
		}
		scanned += read
		position = readOffset
		if position == 0 {
			if scanned+contentBytes > pool.maxLineBytes {
				return pool.lineTooLong(0)
			}
			return nil
		}
	}
	return pool.lineTooLong(max(0, blockStart-int64(remaining)))
}

func (pool *Pool) appendOffset(offset uint32) {
	if len(pool.offsets) == cap(pool.offsets) {
		maximum := int(min(int64(pool.blockBytes), pool.fileSize)) + 1
		capacity := cap(pool.offsets) * 2
		if capacity == 0 {
			capacity = min(maximum, max(1024, maximum/32))
		} else if capacity < cap(pool.offsets) {
			capacity = maximum
		} else {
			capacity = max(capacity, 1024)
			if capacity >= maximum-1 {
				capacity = maximum
			}
		}
		grown := make([]uint32, len(pool.offsets), capacity)
		copy(grown, pool.offsets)
		pool.offsets = grown
	}
	pool.offsets = append(pool.offsets, offset)
}

func (pool *Pool) startCycleLocked(cycle uint64) {
	pool.cycle = cycle
	pool.cycleStartCursor = pool.cursor
	pool.cycleSeed = mix64(pool.seed + cycle*mixIncrement)
	pool.regionCursor = 0
	pool.regionBlockNext = 0
	pool.regionBlocks = 0
	pool.lineCursor = 0
	pool.offsets = pool.offsets[:0]
	if pool.regionCount == 0 {
		pool.nextRegion, pool.regionStep, pool.regionAdvance = 0, 0, 0
		return
	}
	pool.regionStep, pool.nextRegion = permutationParams(pool.regionCount, pool.cycleSeed)
	pool.nextRegion = int64(addModulo(
		uint64(pool.nextRegion),
		multiplyModulo(uint64(pool.regionStep), uint64(pool.shardIndex), uint64(pool.regionCount)),
		uint64(pool.regionCount),
	))
	pool.regionAdvance = int64(multiplyModulo(
		uint64(pool.regionStep),
		uint64(pool.shardCount),
		uint64(pool.regionCount),
	))
}

func (pool *Pool) validateSourceLocked() error {
	if pool.file == nil {
		return ErrClosed
	}
	info, err := pool.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != pool.fileSize || info.ModTime().UnixNano() != pool.modified {
		return ErrSourceChanged
	}
	return nil
}

func (pool *Pool) lineTooLong(offset int64) error {
	return fmt.Errorf("%w at byte %d: limit %d", ErrLineTooLong, offset, pool.maxLineBytes)
}

// Reset rewinds to original cycle and deterministic order. Reset clears a
// terminal read error after validating source metadata.
func (pool *Pool) Reset() error {
	if pool == nil {
		return ErrClosed
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed || pool.file == nil {
		return ErrClosed
	}
	if err := pool.validateSourceLocked(); err != nil {
		pool.terminal = err
		return err
	}
	pool.cursor = 0
	pool.terminal = nil
	pool.seqLine = pool.seqLine[:0]
	pool.seqOffset = 0
	if pool.mode == ModeSequential {
		if _, err := pool.section.Seek(0, io.SeekStart); err != nil {
			pool.terminal = err
			return err
		}
		pool.reader.Reset(pool.section)
		pool.cycle = 0
		return nil
	}
	pool.startCycleLocked(0)
	return nil
}

// Stats returns a coherent snapshot.
func (pool *Pool) Stats() Stats {
	if pool == nil {
		return Stats{Closed: true}
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	retainedBytes := int64(cap(pool.buffer)) + int64(cap(pool.offsets))*4 + int64(cap(pool.seqLine))
	if pool.reader != nil {
		retainedBytes += int64(pool.reader.Size())
	}
	var maximumBytes int64
	if pool.sequentialBytes > 0 {
		maximumBytes = int64(pool.sequentialBytes) + int64(pool.maxLineBytes) + 2
	}
	if pool.mode == ModeShuffled {
		maximumBytes = int64(pool.blockBytes+pool.maxLineBytes+3) + int64(pool.blockBytes+1)*4
	}
	return Stats{
		FileSize:              pool.fileSize,
		Blocks:                pool.blockCount,
		Regions:               pool.regionCount,
		ShardRegions:          pool.regionsForShard,
		Cursor:                pool.cursor,
		Cycle:                 pool.cycle,
		Seed:                  pool.seed,
		RetainedBytes:         retainedBytes,
		MaxRetainedBytes:      maximumBytes,
		SequentialBufferBytes: pool.sequentialBytes,
		BlockBytes:            pool.blockBytes,
		RegionBytes:           pool.regionBytes,
		MaxLineBytes:          pool.maxLineBytes,
		ShardCount:            pool.shardCount,
		ShardIndex:            pool.shardIndex,
		Mode:                  pool.mode,
		Reuse:                 pool.reuse,
		Closed:                pool.closed || pool.file == nil,
	}
}

// Close releases file and buffers. Calls after first return nil.
func (pool *Pool) Close() error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed || pool.file == nil {
		pool.closed = true
		return nil
	}
	pool.closed = true
	err := pool.file.Close()
	pool.file = nil
	pool.section = nil
	pool.reader = nil
	pool.buffer = nil
	pool.offsets = nil
	pool.seqLine = nil
	return err
}

func trimLineEnding(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
	}
	return line
}

func exceedsLineLimit(line []byte, limit int) bool {
	length := len(line)
	if length <= limit {
		return false
	}
	if line[length-1] != '\n' {
		return true
	}
	length--
	if length <= limit {
		return false
	}
	return length-1 > limit || line[length-1] != '\r'
}

func permutationParams(count int64, seed uint64) (int64, int64) {
	if count <= 1 {
		return 0, 0
	}
	step := mix64(seed)%uint64(count-1) + 1
	for greatestCommonDivisor(step, uint64(count)) != 1 {
		step++
		if step == uint64(count) {
			step = 1
		}
	}
	return int64(step), int64(mix64(seed^mixIncrement) % uint64(count))
}

func greatestCommonDivisor(left, right uint64) uint64 {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func mix64(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	return value ^ value>>31
}

func addModulo(left, right, modulus uint64) uint64 {
	if modulus <= 1 {
		return 0
	}
	if left >= modulus-right {
		return left - (modulus - right)
	}
	return left + right
}

func multiplyModulo(left, right, modulus uint64) uint64 {
	if modulus <= 1 {
		return 0
	}
	left %= modulus
	var product uint64
	for right != 0 {
		if right&1 != 0 {
			product = addModulo(product, left, modulus)
		}
		right >>= 1
		if right != 0 {
			left = addModulo(left, left, modulus)
		}
	}
	return product
}

func shardItemCount(total, shards, shard int64) int64 {
	if shard >= total {
		return 0
	}
	return (total-1-shard)/shards + 1
}

func ceilingQuotient(value, divisor int64) int64 {
	if value == 0 {
		return 0
	}
	return (value-1)/divisor + 1
}

func boundedSequentialBuffer(size int, fileSize int64) int {
	size = max(16, size)
	if fileSize < int64(size) {
		return max(16, int(fileSize)+1)
	}
	return size
}

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
