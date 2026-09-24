<div align="center">

# proxykit-go

**Fast, allocation-aware Go tools for HTTP and SOCKS proxies.**

[![Go Reference](https://pkg.go.dev/badge/github.com/colduction/proxykit-go.svg)](https://pkg.go.dev/github.com/colduction/proxykit-go)
[![Go 1.27+](https://img.shields.io/badge/go-1.27%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![Dependencies: none](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)
[![License](https://img.shields.io/github/license/colduction/proxykit-go)](LICENSE)

Validate &middot; Parse &middot; Iterate

[Install](#install) &middot; [Packages](#packages) &middot; [Proxy](#proxy) &middot; [Parser](#parser) &middot; [Pool](#pool)

</div>

> [!NOTE]
> Parsers and pools are safe for concurrent use. Caller-owned destinations and
> buffers require separate ownership.

## Install

```bash
go get github.com/colduction/proxykit-go@latest
```

## Packages

| Package                                                                           | Purpose                                                 |
| --------------------------------------------------------------------------------- | ------------------------------------------------------- |
| [`proxykit`](https://pkg.go.dev/github.com/colduction/proxykit-go)                | Proxy model, URL export, validation                     |
| [`proxyparser`](https://pkg.go.dev/github.com/colduction/proxykit-go/proxyparser) | Compiled custom-format parser                           |
| [`proxypool`](https://pkg.go.dev/github.com/colduction/proxykit-go/proxypool)     | Concurrent iteration over newline-delimited proxy files |

## Proxy

```go
package main

import (
	"fmt"

	"github.com/colduction/proxykit-go"
)

func main() {
	proxy := proxykit.Proxy{
		Scheme:   proxykit.HTTP,
		Host:     "proxy.example.com:8080",
		Username: "user",
		Password: "pass",
	}
	if proxy.IsValid() {
		fmt.Println(proxy.ExportURL().Redacted())
	}
}
```

Supported schemes: `http`, `https`, `socks4`, `socks4a`, `socks5`, `socks5h`.
`https` is an HTTP proxy reached over TLS. `socks4a` and `socks5h` ask the proxy
to resolve host names; note that net/http treats `socks5` like `socks5h`.
`ParseScheme` folds case, and `DefaultPort` reports 80, 443, or 1080.

> [!IMPORTANT]
> Use `host:port` for DNS/IPv4 and `[ipv6]:port` for IPv6.

Host validation accepts ASCII DNS names, IPv4, and IPv6. DNS labels follow LDH
rules, with 63-byte labels and DNS presentation-length limits. Names whose
final label is all digits must be valid dotted-decimal IPv4. Root-dot names
are supported. IPv6 zone IDs use the raw form (`[fe80::1%eth0]:80`) with
unreserved characters only; `ExportURL` keeps that form in `url.URL.Host`, and
`url.URL.String` percent-encodes it.

Helpers include `IsValidScheme`, `IsValidHost`, `IsValidHostnamePort`,
`IsValidCredentials`, and `SplitHostnamePort`.

`Validate` returns the first failing sentinel (`ErrInvalidScheme`, `ErrInvalidHost`,
`ErrInvalidPort`, `ErrInvalidCredentials`) and allocates only when rejecting a
malformed bracketed IPv6 literal; `IsValid` is its boolean form.

`FromURL` is the inverse of `ExportURL` for URLs from `url.Parse` or
`http.ProxyFromEnvironment`, filling a missing or empty port from
`DefaultPort`.

Credentials are printable ASCII of at most 255 bytes each. `IsValidCredentialsFor`
adds scheme rules: no colon in an HTTP Basic username (RFC 7617), and no password
for SOCKS4 or SOCKS4A. A `Proxy` or `*Proxy` logged through `log/slog` as an
attribute value redacts its password via `LogValue`; slices and struct fields
holding one, and every encoder tag, serialize it in plaintext.

## Parser

Compile once, reuse across goroutines:

```go
parser, err := proxyparser.New("%t://%u:%p@%h:%d", true)
if err != nil {
	return err
}

var proxy proxykit.Proxy
if err := parser.ParseInto(
	"socks5://user:pass@proxy.example.com:1080",
	&proxy,
); err != nil {
	return err
}
```

Supported verbs:

| Verb | Field       |
| ---- | ----------- |
| `%t` | Scheme      |
| `%h` | Host        |
| `%d` | Port        |
| `%u` | Username    |
| `%p` | Password    |
| `%%` | Literal `%` |

`strict=true` requires an exact format match. Lenient mode accepts omitted
credentials, including a username without a password, and ignores delimiter
mismatches and trailing input. The result must still pass `Validate`, so a port
is always required. Bracketed IPv6 hosts also work with custom delimiters.

Use `ParseString` for a value result, `ParseInto` for caller-owned reuse, and
`ParseBytes` to avoid copying the input into a string. Joining nonadjacent host
and port fields still allocates a string.

> [!WARNING]
> `ParseBytes` aliases its input. Keep input immutable while parsed fields remain
> in use; concurrent calls need distinct destinations.

Use `errors.Is` for sentinels such as `ErrInvalidProxyFormat`, and
`errors.As`/`errors.AsType` for typed parse errors. `ErrInvalidProxyFormat` wraps
the `proxykit` sentinel that failed, so `errors.Is(err, proxykit.ErrInvalidPort)`
also works. Scheme verbs match case-insensitively and store the lowercase form.

### Performance

Valid input in a common shape (lowercase scheme, DNS name or dotted IPv4 host)
takes a fast path that tests eight bytes per machine word. For a format with
credentials or custom delimiters and input of at most 64 bytes, it first builds
a structural index, one bitmap per byte class, with a vector kernel, then
resolves delimiters and whole ranges with bit operations. Every tier produces
identical results, and the byte-wise parser still decides everything else,
including every validation error.

| Tier           | Where                                      | How                                       |
| -------------- | ------------------------------------------ | ----------------------------------------- |
| AVX-512 / AVX2 | amd64, detected at start, input ≤ 64 B     | hand-written Go assembly, no dependencies |
| NEON           | arm64, input ≤ 64 B                        | hand-written Go assembly                  |
| Word-at-a-time | every platform, any input length, `purego` | pure Go, eight bytes per 64-bit word      |

The fast path also accepts DNS labels ending in digits, such as `proxy1`.
IPv6 literals and invalid input use the scalar parser. See [performance and
validation](PERFORMANCE.md) for benchmark commands, workloads, and test coverage.

Select a tier in the package benchmarks with
`PROXYKIT_BACKEND=portable|avx2|avx512|neon go test ./proxyparser -bench .`;
build with `-tags purego` to exclude the assembly.

## Pool

`Pool` reads one proxy per line, omitting LF and an optional preceding CR.

```go
pool, err := proxypool.New("proxies.txt", proxypool.ModeSequential, false)
if err != nil {
	return err
}
defer pool.Close()

for {
	line, err := pool.Next()
	if errors.Is(err, io.EOF) {
		break
	}
	if err != nil {
		return err
	}
	consume(line)
}
```

| Mode             | Behavior                                          |
| ---------------- | ------------------------------------------------- |
| `ModeSequential` | File order, one block at a time                   |
| `ModeShuffled`   | Locality-preserving region/block/line permutation |

Both modes read the file one block at a time, 1 MiB sequential and 4 MiB
shuffled by default, index the block's line feeds with a vector kernel, and
keep one block plus 4-byte line offsets. A pool opens in constant time, loads
blocks lazily, and uses no sidecar index, memory map, or goroutine. Shuffled
order is not a uniform global line permutation. `MaxLineBytes` bounds returned
line size; an oversized line returns `ErrLineTooLong` when its block loads,
which may precede earlier lines of that block.

> [!CAUTION]
> Source files must remain immutable while a pool is open.

Reuse caller storage with `NextBytes`:

```go
buf := make([]byte, 0, 128)
for {
	var err error
	buf, err = pool.NextBytes(buf[:0])
	if errors.Is(err, io.EOF) {
		break
	}
	if err != nil {
		return err
	}
	consume(buf)
}
```

Take a whole block at a time with `NextBatch`, the fastest way through a file.
It hands the block over under one lock, and `Lines` yields views of its lines
in the order `Next` would return them, without a copy or an allocation once
the batch is warm:

```go
var batch proxypool.Batch
for {
	if err := pool.NextBatch(&batch); err != nil {
		if errors.Is(err, io.EOF) {
			break
		}
		return err
	}
	for line := range batch.Lines() {
		consume(line) // valid until batch is passed to NextBatch again
	}
}
```

Configure pools with `Open`:

```go
pool, err := proxypool.Open("proxies.txt", proxypool.Options{
	Mode:         proxypool.ModeShuffled,
	Reuse:        true,
	BlockBytes:   4 << 20,
	RegionBytes:  1 << 30,
	MaxLineBytes: 64 << 10,
	Seed:         12345,
	Prefetch:     true,
})
```

`Prefetch` reads ahead while a block is consumed, for files the system cache
does not hold. Linux gets `POSIX_FADV_WILLNEED` and macOS `F_RDADVISE` for the
next block. On Windows, `NextBatch` keeps overlapped reads of the next blocks
in flight on a second handle, unbuffered when the file is larger than the
memory available to cache it; a pool then keeps up to three more blocks.

`Stats` reports file size, blocks, regions, cursor, cycle, seed, retained and
maximum memory, limits, shard configuration, prefetching, and closed state.
`Reset` restores cycle zero and clears terminal read errors after source
validation. Size and modification-time checks run at bounded checkpoints.
Non-EOF errors remain terminal until `Reset` succeeds.

For parallel storage reads, use pools with matching source, `BlockBytes`,
`RegionBytes`, nonzero `Seed`, and `ShardCount`, assigning each
`ShardIndex` in `[0, ShardCount)`. Shards collectively return every line once.
`Reuse` cannot be combined with sharding.

## License

Licensed under the [MIT License](LICENSE).
