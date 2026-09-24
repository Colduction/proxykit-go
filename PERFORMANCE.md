# Performance and validation

## Correctness

Regression tests cover these behaviors:

- IPv6 zone IDs are validated separately from DNS names. A trailing dot belongs
  to the zone, and DNS name length limits do not constrain it. Validation does
  not intern zone strings.
- `FromURL` preserves an explicitly invalid port for validation.
- Custom parser delimiters do not split inside bracketed IPv6 addresses.
- Lenient `%u:%p@` groups accept a username without a password while preserving
  usernames containing `@` when a password separator is present.
- `Batch.Lines` detects batch replacement after every callback, including early
  exit and the last line. A callback panic preserves the starting position.
- Pool options account for buffer alignment padding before converting sizes to
  platform-sized allocations, including on 32-bit systems.
- Windows positional reads reject negative offsets, including Windows' special
  current-position offset of -2.
- Unix pools reject FIFOs without waiting for a writer. The opened descriptor
  is still checked for regular-file type.
- Plan 9 builds use `errors.ErrUnsupported` for unavailable advisory calls.

The pool remembers one bounded interval of blocks that a successfully completed
line proves contain no line starts. It skips their reads without changing region
order, sharding, metadata-check cadence, or cycle termination. The interval is
cleared on reset and each new cycle. Prefetch follows the same exclusion rule.
This uses two `int64` fields per pool and no file-sized index.

## Benchmarking

Run benchmarks on the target hardware with the intended Go toolchain:

```powershell
go test . -run '^$' -bench '^Benchmark(IsValidHostnamePort|IsValidHost|SplitHostnamePort|ProxyValidatorMethods|ExportURL|PoolParse)$' -benchmem -benchtime=200ms -cpu=1 -count=10
go test ./proxyparser -run '^$' -bench '^BenchmarkParseInto(_(Shapes|Mixed)|NumericSuffix)$' -benchmem -benchtime=200ms -cpu=1 -count=10
go test ./proxypool -run '^$' -bench '^Benchmark(NextBytes|NextBatch|BatchLines|BatchIterate|ReadCycleBatch|ShuffledContinuation|SequentialContinuation)$' -benchmem -benchtime=200ms -cpu=1 -count=10
```

The parser and pool test binaries honor `PROXYKIT_BACKEND`; the root integration
benchmark selects the CPU backend automatically. Use `-tags purego` for a
portable comparison across all packages. Keep hardware, toolchain, backend,
workloads, and sampling settings consistent when comparing changes.

The file-and-parser workload scans a warm 4096-line file with equal shares of
numeric-suffix DNS names, other DNS names, IPv4, and IPv6. It verifies line count
and a host-length checksum; preflight compares every parsed proxy with its
expected fields. Pool benchmarks cover sequential and shuffled access, LF/CRLF,
batch iteration, and long lines spanning small blocks.

These benchmarks measure single-goroutine, warm-cache throughput. They do not
measure uncached device throughput, contention latency percentiles, or production
load. Shuffled order is a locality-preserving permutation, not a uniform global
shuffle. Allocation counts can round down when allocations are amortized across
operations; inspect bytes per operation as well.

## Test coverage

- Local HTTP and certificate-verified HTTPS proxy integration covers file
  loading, batch iteration, parsing, URL escaping, and authenticated requests.
  Tests use local listeners and require no external proxy service.
- Parser fuzz targets cover scalar parity, custom IPv6 fields, and entry-point
  parity. Pool oracle fuzzing covers file boundaries, sharding, reset, prefetch,
  and mixed APIs. Host validation and line-index operations have fuzz coverage.
- Guard-page tests exercise SIMD bounds. Windows tests cover buffered and
  unbuffered prefetch, cancellation, truncation, and buffer reuse.
- Linux tests verify that opening a FIFO does not wait for a writer.

Run `go test ./...`, `go test -race ./...`, and `go test -tags purego ./...` on
supported hosts. Use `go vet ./...`, `staticcheck ./...`, and `govulncheck ./...`
for additional checks. Cross-compilation checks build compatibility; runtime
behavior requires tests on the target platform.

## Contract references

- [Go URL.Port](https://pkg.go.dev/net/url#URL.Port), including its filtering of
  nonnumeric port text.
- [Go File.ReadAt](https://go.dev/src/os/file.go) for positional-read error behavior.
- [RFC 4007 section 11](https://www.rfc-editor.org/rfc/rfc4007.html#section-11)
  for scoped IPv6 address text, alongside this library's documented zone alphabet.
- [Win32 ReadFile](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-readfile)
  and [pending I/O cancellation](https://learn.microsoft.com/en-us/windows/win32/fileio/canceling-pending-i-o-operations)
  for buffer lifetime, overlapped completion, and cancellation ownership.
