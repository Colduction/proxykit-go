// Package proxypool provides bounded-memory iteration over newline-delimited
// proxy files.
package proxypool

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"runtime"
	"sync"

	"github.com/colduction/proxykit-go/internal/blockread"
	"github.com/colduction/proxykit-go/internal/lineindex"
)

const (
	// DefaultSequentialBufferBytes is the default sequential block size: the
	// read granularity and retained data-buffer size of a sequential pool.
	DefaultSequentialBufferBytes = 1 << 20

	// DefaultBlockBytes is the default shuffled locality-block size.
	DefaultBlockBytes = 4 << 20

	// DefaultRegionBytes is the default shuffled locality-region size.
	DefaultRegionBytes int64 = 1 << 30

	// DefaultMaxLineBytes is the default maximum proxy size, excluding CRLF or LF.
	DefaultMaxLineBytes = 64 << 10

	tailReadBytes = 4 << 10
	lineReadBytes = 64 << 10
	mixIncrement  = 0x9e3779b97f4a7c15
)

var (
	// ErrClosed reports use of a closed pool.
	ErrClosed = errors.New("proxypool: pool closed")

	// ErrSourceChanged reports that source size or modification time changed.
	ErrSourceChanged = errors.New("proxypool: source file changed")

	// ErrLineTooLong reports a line larger than [Options.MaxLineBytes].
	ErrLineTooLong = errors.New("proxypool: line too long")

	// ErrNilBatch reports a nil [Batch] passed to [Pool.NextBatch].
	ErrNilBatch = errors.New("proxypool: nil batch")
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
	// SequentialBufferBytes sets the sequential block size: the read
	// granularity and the retained data-buffer size. Values below one use
	// [DefaultSequentialBufferBytes]. Small values trade read calls for memory.
	SequentialBufferBytes int `json:"sequentialBufferBytes,omitempty" yaml:"sequentialBufferBytes,omitempty" xml:"sequentialBufferBytes,omitempty" cbor:"sequentialBufferBytes,omitempty" bson:"sequentialBufferBytes,omitempty" msgpack:"sequentialBufferBytes,omitempty" toml:"sequentialBufferBytes,omitempty" mapstructure:"sequentialBufferBytes,omitempty"`

	// BlockBytes sets shuffled read granularity and retained data-buffer size.
	// Values below one use [DefaultBlockBytes]. Sequential mode ignores it.
	// Keep BlockBytes at least MaxLineBytes+2 when long lines are common to
	// reduce continuation reads.
	BlockBytes int `json:"blockBytes,omitempty" yaml:"blockBytes,omitempty" xml:"blockBytes,omitempty" cbor:"blockBytes,omitempty" bson:"blockBytes,omitempty" msgpack:"blockBytes,omitempty" toml:"blockBytes,omitempty" mapstructure:"blockBytes,omitempty"`

	// RegionBytes sets shuffled seek locality. Values below one use
	// [DefaultRegionBytes]. Effective size is a whole number of blocks. Larger
	// regions reduce seeks; smaller regions produce finer global mixing.
	// Sequential mode ignores it.
	RegionBytes int64 `json:"regionBytes,omitempty" yaml:"regionBytes,omitempty" xml:"regionBytes,omitempty" cbor:"regionBytes,omitempty" bson:"regionBytes,omitempty" msgpack:"regionBytes,omitempty" toml:"regionBytes,omitempty" mapstructure:"regionBytes,omitempty"`

	// MaxLineBytes bounds one proxy, excluding one LF and optional preceding CR.
	// Values below one use [DefaultMaxLineBytes]. This bound is required because
	// no line-returning API can promise bounded memory for an unbounded line.
	// The block size plus MaxLineBytes+3 must fit in 32 bits.
	// That size including buffer alignment padding must also fit in int.
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

	// Prefetch asks the kernel to read ahead in the background while the
	// current block is consumed, so that storage latency overlaps work when
	// the file is not cached. It starts no goroutine. On Linux and macOS it
	// sends POSIX_FADV_WILLNEED or F_RDADVISE for the next block and retains
	// no Go memory; 32-bit Linux lacks the call. On Windows, [Pool.NextBatch]
	// keeps overlapped reads of the next blocks in flight on a second handle
	// while the caller consumes a [Batch], and reads them without buffering
	// when the file is larger than the memory available to cache it and its
	// blocks cover whole pages, which the defaults do. A pool that serves only
	// batches then keeps two blocks besides the caller's, and
	// [Stats.MaxRetainedBytes] allows for three more than without Prefetch;
	// the Next and NextBytes paths read synchronously. The other platforms
	// ignore it. A request the kernel rejects turns it off for the pool;
	// [Stats.Prefetch] reports whether it is active.
	Prefetch bool `json:"prefetch,omitempty" yaml:"prefetch,omitempty" xml:"prefetch,omitempty" cbor:"prefetch,omitempty" bson:"prefetch,omitempty" msgpack:"prefetch,omitempty" toml:"prefetch,omitempty" mapstructure:"prefetch,omitempty"`
}

// Stats is a point-in-time pool snapshot. File-size-derived counts use int64,
// so 10 TiB files remain representable on 32-bit and 64-bit platforms.
type Stats struct {
	// FileSize is source size at open time, in bytes.
	FileSize int64 `json:"fileSize" yaml:"fileSize" xml:"fileSize" cbor:"fileSize" bson:"fileSize" msgpack:"fileSize" toml:"fileSize" mapstructure:"fileSize"`

	// Blocks is total physical block count.
	Blocks int64 `json:"blocks" yaml:"blocks" xml:"blocks" cbor:"blocks" bson:"blocks" msgpack:"blocks" toml:"blocks" mapstructure:"blocks"`

	// Regions is total locality-region count before sharding.
	Regions int64 `json:"regions" yaml:"regions" xml:"regions" cbor:"regions" bson:"regions" msgpack:"regions" toml:"regions" mapstructure:"regions"`

	// ShardRegions is number of regions assigned to this pool.
	ShardRegions int64 `json:"shardRegions" yaml:"shardRegions" xml:"shardRegions" cbor:"shardRegions" bson:"shardRegions" msgpack:"shardRegions" toml:"shardRegions" mapstructure:"shardRegions"`

	// Cursor is number of lines returned since open or [Pool.Reset]. Lines
	// handed to a [Batch] count when [Pool.NextBatch] returns.
	Cursor int64 `json:"cursor" yaml:"cursor" xml:"cursor" cbor:"cursor" bson:"cursor" msgpack:"cursor" toml:"cursor" mapstructure:"cursor"`

	// Cycle is zero-based automatic reuse cycle.
	Cycle uint64 `json:"cycle" yaml:"cycle" xml:"cycle" cbor:"cycle" bson:"cycle" msgpack:"cycle" toml:"cycle" mapstructure:"cycle"`

	// Seed is actual shuffle seed, including an automatically selected seed. It
	// is zero in sequential mode.
	Seed uint64 `json:"seed" yaml:"seed" xml:"seed" cbor:"seed" bson:"seed" msgpack:"seed" toml:"seed" mapstructure:"seed"`

	// RetainedBytes is current Go buffer capacity, including alignment padding
	// and excluding small fixed state and the storage of live [Batch] values.
	RetainedBytes int64 `json:"retainedBytes" yaml:"retainedBytes" xml:"retainedBytes" cbor:"retainedBytes" bson:"retainedBytes" msgpack:"retainedBytes" toml:"retainedBytes" mapstructure:"retainedBytes"`

	// MaxRetainedBytes is the configured upper bound for retained data and
	// offset buffers, excluding caller-owned results, live [Batch] values, and
	// small fixed state.
	MaxRetainedBytes int64 `json:"maxRetainedBytes" yaml:"maxRetainedBytes" xml:"maxRetainedBytes" cbor:"maxRetainedBytes" bson:"maxRetainedBytes" msgpack:"maxRetainedBytes" toml:"maxRetainedBytes" mapstructure:"maxRetainedBytes"`

	// SequentialBufferBytes is the effective sequential block size. It is zero
	// in shuffled mode.
	SequentialBufferBytes int `json:"sequentialBufferBytes" yaml:"sequentialBufferBytes" xml:"sequentialBufferBytes" cbor:"sequentialBufferBytes" bson:"sequentialBufferBytes" msgpack:"sequentialBufferBytes" toml:"sequentialBufferBytes" mapstructure:"sequentialBufferBytes"`

	// BlockBytes is the effective block size in either mode.
	BlockBytes int `json:"blockBytes" yaml:"blockBytes" xml:"blockBytes" cbor:"blockBytes" bson:"blockBytes" msgpack:"blockBytes" toml:"blockBytes" mapstructure:"blockBytes"`

	// RegionBytes is the effective locality-region size in either mode.
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

	// Prefetch reports whether the pool asks the kernel to read ahead: it was
	// requested, the platform supports it, and no hint has failed.
	Prefetch bool `json:"prefetch" yaml:"prefetch" xml:"prefetch" cbor:"prefetch" bson:"prefetch" msgpack:"prefetch" toml:"prefetch" mapstructure:"prefetch"`
}

// Pool reads one immutable regular file. Its zero value behaves as closed. A
// Pool must not be copied after first use. A Pool is safe for concurrent use,
// though calls share one cursor and therefore serialize. File I/O occurs while
// holding that cursor lock, so Close, Reset, and other reads wait for an active
// read; reads ahead that [Options.Prefetch] starts on Windows run in the
// kernel between calls. For parallel storage reads, open explicitly
// partitioned pools with [Options.ShardCount].
//
// Both modes read the file one block at a time and keep one data block plus
// uint32 line offsets. With B the block size, BlockBytes in shuffled mode and
// SequentialBufferBytes in sequential mode, retained memory is bounded by
// roughly B + MaxLineBytes + 4*B; each live [Batch] holds one more block, and
// on Windows [Options.Prefetch] may keep up to three more for reads ahead.
// [Stats.MaxRetainedBytes] reports the exact bound. A Pool does not preload
// source data, memory-map the file, build a sidecar, or start goroutines.
//
// Errors of a block, such as [ErrLineTooLong] or a read failure, surface when
// the block loads, so they may precede lines that lie earlier in that block.
type Pool struct {
	noCopy            noCopy
	terminal          error
	file              *os.File
	buffer            []byte
	offsets           []uint32
	fileSize          int64
	modified          int64
	blockCount        int64
	regionCount       int64
	regionsForShard   int64
	regionsPerCheck   int64
	blocksPerRegion   int64
	regionCursor      int64
	nextRegion        int64
	regionStep        int64
	regionAdvance     int64
	regionBlockBase   int64
	regionBlocks      int64
	regionBlockNext   int64
	regionRotation    int64
	carriedBlock      int64
	continuationStart int64
	continuationEnd   int64
	lineCursor        int64
	nextLine          int64
	lineStep          int64
	cursor            int64
	cycleStartCursor  int64
	regionBytes       int64
	cycle             uint64
	seed              uint64
	cycleSeed         uint64
	blockBytes        int
	maxLineBytes      int
	shardCount        int
	shardIndex        int
	reader            blockread.Reader
	hints             blockread.Hints
	ahead             *blockread.Ahead
	aheadCleanup      runtime.Cleanup
	mu                sync.Mutex
	mode              Mode
	reuse             bool
	closed            bool
	cr                bool
	prefetching       bool
	carried           byte
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
// checks detect ordinary changes at exhaustion and after roughly each
// [DefaultRegionBytes] of input, not adversarial same-metadata rewrites.
func Open(path string, options Options) (*Pool, error) {
	err := options.Mode.Valid()
	if err != nil {
		return nil, err
	}
	file, err := blockread.OpenSource(path, options.Mode == ModeSequential)
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
	maxLineBytes, shardCount :=
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
	var (
		blockBytes  int
		regionBytes int64
		seed        uint64
	)
	if options.Mode == ModeSequential {
		blockBytes = positiveOrDefault(options.SequentialBufferBytes, DefaultSequentialBufferBytes)
		regionBytes = max(DefaultRegionBytes, int64(blockBytes))
	} else {
		blockBytes = positiveOrDefault(options.BlockBytes, DefaultBlockBytes)
		regionBytes = options.RegionBytes
		if regionBytes < 1 {
			regionBytes = DefaultRegionBytes
		}
		seed = options.Seed
		if seed == 0 {
			seed = rand.Uint64()
			if seed == 0 {
				seed = mixIncrement
			}
		}
	}
	if blockBytes > maxInt-maxLineBytes-3 {
		file.Close()
		return nil, errors.New("proxypool: block and maximum line sizes overflow int")
	}
	if uint64(blockBytes+maxLineBytes+3) > uint64(^uint32(0)) {
		file.Close()
		return nil, errors.New("proxypool: block and maximum line sizes exceed uint32 offsets")
	}
	if blockread.BufferBytes(blockBytes+maxLineBytes+3) > int64(maxInt) {
		file.Close()
		return nil, errors.New("proxypool: aligned buffer size overflows int")
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
	// Source validation cadence stays independent of the locality setting.
	regionsPerCheck := max(int64(1), DefaultRegionBytes/regionBytes)
	pool := &Pool{
		file:            file,
		fileSize:        info.Size(),
		modified:        info.ModTime().UnixNano(),
		regionBytes:     regionBytes,
		regionsPerCheck: regionsPerCheck,
		seed:            seed,
		blockBytes:      blockBytes,
		maxLineBytes:    maxLineBytes,
		shardCount:      shardCount,
		shardIndex:      options.ShardIndex,
		mode:            options.Mode,
		reuse:           options.Reuse,
	}
	pool.blockCount = ceilingQuotient(info.Size(), int64(blockBytes))
	pool.blocksPerRegion = blocksPerRegion
	pool.regionCount = ceilingQuotient(pool.blockCount, blocksPerRegion)
	pool.regionsForShard = shardItemCount(pool.regionCount, int64(shardCount), int64(options.ShardIndex))
	pool.startCycleLocked(0)
	pool.reader.Init(file)
	pool.prefetching = options.Prefetch && pool.openPrefetch()
	return pool, nil
}

// Next returns next proxy.
// It returns [io.EOF] after exhaustion, [ErrClosed] after close, and the
// underlying I/O or validation error on failure.
// Every returned error remains terminal until a successful [Pool.Reset].
func (pool *Pool) Next() (string, error) {
	if pool == nil {
		return "", ErrClosed
	}
	pool.mu.Lock()
	line, err := pool.nextLocked()
	if err != nil {
		pool.mu.Unlock()
		return "", err
	}
	result := string(line)
	pool.mu.Unlock()
	return result, nil
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
	line, err := pool.nextLocked()
	if err != nil {
		pool.mu.Unlock()
		return dst[:0], err
	}
	result := append(dst[:0], line...)
	pool.mu.Unlock()
	return result, nil
}

func (pool *Pool) nextLocked() ([]byte, error) {
	if pool.closed || pool.file == nil {
		return nil, ErrClosed
	}
	if pool.terminal != nil {
		return nil, pool.terminal
	}
	line, err := pool.nextLineLocked()
	if err != nil {
		pool.terminal = err
	}
	return line, err
}

func (pool *Pool) nextLineLocked() ([]byte, error) {
	for pool.lineCursor >= int64(len(pool.offsets))-1 {
		if err := pool.loadNextBlockLocked(); err != nil {
			return nil, err
		}
	}
	index := pool.nextLine
	start, end := pool.offsets[index], pool.offsets[index+1]
	lines := int64(len(pool.offsets)) - 1
	index -= lines - pool.lineStep
	pool.nextLine = index + lines&(index>>63)
	pool.lineCursor++
	pool.cursor++
	line := pool.buffer[start : end-1]
	if pool.cr && len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
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
			if block >= pool.continuationStart && block < pool.continuationEnd {
				continue
			}
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
		if pool.regionCursor > 0 && pool.regionCursor%pool.regionsPerCheck == 0 {
			if err := pool.validateSourceLocked(); err != nil {
				return err
			}
		}
		region := pool.nextRegion
		pool.regionCursor++
		pool.nextRegion = int64(addModulo(uint64(pool.nextRegion), uint64(pool.regionAdvance), uint64(pool.regionCount)))
		pool.regionBlockBase = region * pool.blocksPerRegion
		pool.regionBlocks = min(pool.blocksPerRegion, pool.blockCount-pool.regionBlockBase)
		pool.regionBlockNext = 0
		pool.regionRotation = pool.rotationLocked(region, pool.regionBlocks)
	}
}

func (pool *Pool) rotationLocked(region, blocks int64) int64 {
	if pool.mode == ModeSequential {
		return 0
	}
	return int64(mix64(pool.cycleSeed^uint64(region)) % uint64(blocks))
}

func (pool *Pool) upcomingBlocksLocked(blocks []int64) int {
	next, count := pool.regionBlockNext, pool.regionBlocks
	base, rotation := pool.regionBlockBase, pool.regionRotation
	cursor, region := pool.regionCursor, pool.nextRegion
	var written int
	for written < len(blocks) {
		if next == count {
			if cursor == pool.regionsForShard {
				return written
			}
			base = region * pool.blocksPerRegion
			count = min(pool.blocksPerRegion, pool.blockCount-base)
			rotation = pool.rotationLocked(region, count)
			next = 0
			cursor++
			region = int64(addModulo(uint64(region), uint64(pool.regionAdvance), uint64(pool.regionCount)))
		}
		block := base + int64(addModulo(uint64(rotation), uint64(next), uint64(count)))
		next++
		if block >= pool.continuationStart && block < pool.continuationEnd {
			continue
		}
		blocks[written] = block
		written++
	}
	return len(blocks)
}

func (pool *Pool) openPrefetch() bool {
	direct := pool.blockBytes%blockread.PageBytes == 0 && pool.blockBytes >= blockread.AlignedBytes &&
		pool.lookaheadBytes() == blockread.PageBytes && pool.fileSize >= 4*int64(pool.blockBytes)
	if pool.ahead = pool.reader.OpenAhead(pool.fileSize, pool.mode == ModeSequential, direct); pool.ahead != nil {
		pool.aheadCleanup = runtime.AddCleanup(pool, (*blockread.Ahead).Close, pool.ahead)
		return true
	}
	return pool.hints.Open(pool.file)
}

func (pool *Pool) prefetchNextLocked() {
	var next [1]int64
	if pool.upcomingBlocksLocked(next[:]) == 0 {
		return
	}
	readOffset, skip, _, readBytes := pool.blockReadRange(next[0], pool.carriedBlock)
	if !pool.hints.Advise(readOffset+int64(skip), readBytes-skip) {
		pool.prefetching = false
	}
}

func (pool *Pool) lookaheadBytes() int {
	return min(tailReadBytes, pool.maxLineBytes+2, pool.blockBytes/8)
}

func (pool *Pool) blockReadRange(block, carried int64) (readOffset int64, skip, ownershipEnd, readBytes int) {
	blockStart := block * int64(pool.blockBytes)
	baseBytes := int(min(int64(pool.blockBytes), pool.fileSize-blockStart))
	readOffset, ownershipEnd = blockStart-1, 1+baseBytes
	if block == 0 || block == carried {
		skip = 1
	}
	lookahead := pool.lookaheadBytes()
	if remaining := pool.fileSize - blockStart - int64(baseBytes); int64(lookahead) > remaining {
		lookahead = int(remaining)
	}
	return readOffset, skip, ownershipEnd, ownershipEnd + lookahead
}

func (pool *Pool) loadBlockLocked(block int64) error {
	readOffset, skip, ownershipEnd, readBytes := pool.blockReadRange(block, pool.carriedBlock)
	blockStart := readOffset + 1
	read, adopted, err := pool.adoptLocked(block, readOffset+int64(skip), skip, readBytes)
	if !adopted {
		pool.resizeBuffer(readBytes)
		read, err = pool.reader.ReadAt(pool.buffer[skip:readBytes], readOffset+int64(skip))
	}
	if read != readBytes-skip {
		if changed := pool.validateSourceLocked(); changed != nil {
			return changed
		}
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("proxypool: read block at %d: %w", blockStart, err)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("proxypool: read block at %d: %w", blockStart, err)
	}
	if skip == 1 {
		pool.buffer[0] = pool.carried
		if block == 0 {
			pool.buffer[0] = '\n'
		}
	}
	pool.carriedBlock, pool.carried = block+1, pool.buffer[ownershipEnd-1]
	if pool.prefetching && pool.ahead == nil {
		pool.prefetchNextLocked()
	}
	pool.offsets = pool.offsets[:0]
	pool.cr = false
	pool.lineCursor, pool.nextLine = 0, 0
	lineStart := 1
	if pool.buffer[0] != '\n' {
		newline := bytes.IndexByte(pool.buffer[1:ownershipEnd], '\n')
		if newline < 0 {
			return nil
		}
		lineStart = 2 + newline
	}
	if lineStart >= ownershipEnd {
		return nil
	}
	pool.appendOffset(uint32(lineStart))
	lastEnd := pool.indexBlockLocked(lineStart, ownershipEnd)
	if err := pool.checkLineLimitLocked(readOffset); err != nil {
		return err
	}
	if lastEnd < ownershipEnd {
		if blockStart+int64(ownershipEnd-1) < pool.fileSize {
			lineEnd, err := pool.extendLineLocked(readOffset, lastEnd, ownershipEnd)
			if err != nil {
				return err
			}
			nextStart := readOffset + int64(lineEnd)
			pool.continuationStart = block + 1
			pool.continuationEnd = nextStart / int64(pool.blockBytes)
			if nextStart >= pool.fileSize {
				pool.continuationEnd = pool.blockCount
			}
			pool.appendOffset(uint32(lineEnd))
		} else {
			pool.buffer = pool.buffer[:ownershipEnd]
			if exceedsLineLimit(pool.buffer[lastEnd:ownershipEnd], pool.maxLineBytes) {
				return pool.lineTooLong(readOffset + int64(lastEnd))
			}
			pool.appendOffset(pool.terminateLocked(ownershipEnd))
		}
	}
	lines := int64(len(pool.offsets) - 1)
	if pool.mode == ModeSequential {
		pool.lineStep, pool.nextLine = 1, 0
	} else {
		pool.lineStep, pool.nextLine = permutationParams(lines, mix64(pool.cycleSeed^uint64(block)))
	}
	return nil
}

func (pool *Pool) indexBlockLocked(lineStart, end int) int {
	for position := lineStart; position < end; {
		if cap(pool.offsets)-len(pool.offsets) < 64 {
			pool.growOffsets()
		}
		written, consumed, cr := lineindex.Ends(pool.offsets[len(pool.offsets):cap(pool.offsets)], pool.buffer[position:end], uint32(position))
		pool.offsets = pool.offsets[:len(pool.offsets)+written]
		pool.cr = pool.cr || cr || pool.buffer[position-1] == '\r'
		position += consumed
	}
	return int(pool.offsets[len(pool.offsets)-1])
}

func (pool *Pool) checkLineLimitLocked(readOffset int64) error {
	offsets := pool.offsets
	if uint64(lineindex.MaxGap(offsets)) <= uint64(pool.maxLineBytes)+1 {
		return nil
	}
	for i := 0; i+1 < len(offsets); i++ {
		if exceedsLineLimit(pool.buffer[offsets[i]:offsets[i+1]], pool.maxLineBytes) {
			return pool.lineTooLong(readOffset + int64(offsets[i]))
		}
	}
	return nil
}

func (pool *Pool) terminateLocked(end int) uint32 {
	if pool.buffer[end-1] != '\r' {
		pool.resizeBuffer(end + 1)
		pool.buffer[end] = '\n'
		return uint32(end + 1)
	}
	pool.resizeBuffer(end + 2)
	pool.buffer[end], pool.buffer[end+1] = '\r', '\n'
	pool.cr = true
	return uint32(end + 2)
}

func (pool *Pool) extendLineLocked(readOffset int64, lineStart, searchStart int) (int, error) {
	limit := lineStart + pool.maxLineBytes + 2
	if newline := bytes.IndexByte(pool.buffer[searchStart:], '\n'); newline >= 0 {
		lineEnd := searchStart + newline + 1
		pool.buffer = pool.buffer[:lineEnd]
		if exceedsLineLimit(pool.buffer[lineStart:lineEnd], pool.maxLineBytes) {
			return 0, pool.lineTooLong(readOffset + int64(lineStart))
		}
		pool.cr = pool.cr || pool.buffer[lineEnd-2] == '\r'
		return lineEnd, nil
	}
	for len(pool.buffer) < limit {
		absoluteEnd := readOffset + int64(len(pool.buffer))
		if absoluteEnd == pool.fileSize {
			if exceedsLineLimit(pool.buffer[lineStart:], pool.maxLineBytes) {
				return 0, pool.lineTooLong(readOffset + int64(lineStart))
			}
			return int(pool.terminateLocked(len(pool.buffer))), nil
		}
		readBytes := min(lineReadBytes, max(tailReadBytes, pool.blockBytes), limit-len(pool.buffer))
		if remaining := pool.fileSize - absoluteEnd; int64(readBytes) > remaining {
			readBytes = int(remaining)
		}
		oldLength := len(pool.buffer)
		pool.resizeBuffer(oldLength + readBytes)
		read, err := pool.reader.ReadAt(pool.buffer[oldLength:], absoluteEnd)
		pool.buffer = pool.buffer[:oldLength+read]
		if newline := bytes.IndexByte(pool.buffer[oldLength:], '\n'); newline >= 0 {
			lineEnd := oldLength + newline + 1
			pool.buffer = pool.buffer[:lineEnd]
			if exceedsLineLimit(pool.buffer[lineStart:lineEnd], pool.maxLineBytes) {
				return 0, pool.lineTooLong(readOffset + int64(lineStart))
			}
			pool.cr = pool.cr || pool.buffer[lineEnd-2] == '\r'
			return lineEnd, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("proxypool: extend line at %d: %w", readOffset+int64(lineStart), err)
		}
		if read != readBytes {
			if readOffset+int64(len(pool.buffer)) == pool.fileSize {
				continue
			}
			if changed := pool.validateSourceLocked(); changed != nil {
				return 0, changed
			}
			return 0, fmt.Errorf("proxypool: extend line at %d: %w", readOffset+int64(lineStart), io.ErrUnexpectedEOF)
		}
	}
	return 0, pool.lineTooLong(readOffset + int64(lineStart))
}

func (pool *Pool) resizeBuffer(length int) {
	pool.buffer = pool.resized(pool.buffer, length)
}

func (pool *Pool) resized(buffer []byte, length int) []byte {
	if cap(buffer) >= length {
		return buffer[:length]
	}
	maximum := pool.blockBytes + pool.maxLineBytes + 3
	capacity := length + 2
	if cap(buffer) == 0 && pool.fileSize > int64(pool.blockBytes) {
		capacity = max(capacity, min(maximum, pool.blockBytes+2+pool.lookaheadBytes()))
	} else if cap(buffer) > 0 {
		base := min(pool.blockBytes+2, maximum)
		tailCapacity := max(0, cap(buffer)-base)
		nextTailCapacity, maximumTailCapacity := tailCapacity*2, maximum-base
		if nextTailCapacity < tailReadBytes {
			nextTailCapacity = tailReadBytes
		}
		if nextTailCapacity < tailCapacity || nextTailCapacity > maximumTailCapacity {
			nextTailCapacity = maximumTailCapacity
		}
		capacity = max(capacity, base+nextTailCapacity)
	}
	if capacity > maximum || capacity < cap(buffer) {
		capacity = maximum
	}
	grown := blockread.MakeBuffer(len(buffer), capacity)
	copy(grown, buffer)
	return grown[:length]
}

func (pool *Pool) appendOffset(offset uint32) {
	if len(pool.offsets) == cap(pool.offsets) {
		pool.growOffsets()
	}
	pool.offsets = append(pool.offsets, offset)
}

func (pool *Pool) growOffsets() {
	maximum := int(min(int64(pool.blockBytes), pool.fileSize)) + 1
	if cap(pool.offsets) >= maximum {
		return
	}
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

func (pool *Pool) startCycleLocked(cycle uint64) {
	pool.cycle = cycle
	pool.cycleStartCursor = pool.cursor
	pool.cycleSeed = mix64(pool.seed + cycle*mixIncrement)
	pool.regionCursor = 0
	pool.regionBlockNext = 0
	pool.regionBlocks = 0
	pool.continuationStart, pool.continuationEnd = 0, 0
	pool.lineCursor = 0
	pool.nextLine = 0
	pool.offsets = pool.offsets[:0]
	pool.cr = false
	if pool.regionCount == 0 {
		pool.nextRegion, pool.regionStep, pool.regionAdvance = 0, 0, 0
		return
	}
	if pool.mode == ModeSequential {
		pool.nextRegion, pool.regionStep, pool.regionAdvance = 0, 1, 1
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
	var maximumBytes int64
	if pool.blockBytes > 0 {
		buffers := int64(1)
		if pool.ahead != nil {
			buffers += blockread.AheadDepth
		}
		maximumBytes = blockread.BufferBytes(pool.blockBytes+pool.maxLineBytes+3)*buffers + int64(pool.blockBytes+1)*4
	}
	var sequentialBytes int
	if pool.mode == ModeSequential {
		sequentialBytes = pool.blockBytes
	}
	return Stats{
		FileSize:              pool.fileSize,
		Blocks:                pool.blockCount,
		Regions:               pool.regionCount,
		ShardRegions:          pool.regionsForShard,
		Cursor:                pool.cursor,
		Cycle:                 pool.cycle,
		Seed:                  pool.seed,
		RetainedBytes:         blockread.BufferBytes(cap(pool.buffer)) + pool.ahead.RetainedBytes() + int64(cap(pool.offsets))*4,
		MaxRetainedBytes:      maximumBytes,
		SequentialBufferBytes: sequentialBytes,
		BlockBytes:            pool.blockBytes,
		RegionBytes:           pool.regionBytes,
		MaxLineBytes:          pool.maxLineBytes,
		ShardCount:            pool.shardCount,
		ShardIndex:            pool.shardIndex,
		Mode:                  pool.mode,
		Reuse:                 pool.reuse,
		Closed:                pool.closed || pool.file == nil,
		Prefetch:              pool.prefetching && !pool.closed && pool.file != nil,
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
	if pool.ahead != nil {
		pool.aheadCleanup.Stop()
		pool.ahead.Close()
		pool.ahead = nil
	}
	err := pool.file.Close()
	pool.file = nil
	pool.buffer = nil
	pool.offsets = nil
	pool.hints = blockread.Hints{}
	pool.prefetching = false
	return err
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

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
