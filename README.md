<div align="center">

# proxykit-go

**Fast, allocation-aware Go tools for HTTP and SOCKS proxies.**

[![Go Reference](https://pkg.go.dev/badge/github.com/colduction/proxykit-go.svg)](https://pkg.go.dev/github.com/colduction/proxykit-go)
![Go 1.27+](https://img.shields.io/badge/go-1.27%2B-00ADD8?logo=go&logoColor=white)
![Dependencies: none](https://img.shields.io/badge/dependencies-none-brightgreen)
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

> [!TIP]
> Requires Go 1.27 or later.

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

`strict=true` requires an exact format match. Lenient mode tolerates missing
optional credentials after parsing scheme and host and ignores delimiter
mismatches and trailing input; the result must still pass `Validate`, so a port
is always required.

Use `ParseString` for a value result, `ParseInto` for caller-owned reuse, and
`ParseBytes` for zero-copy input.

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

`ParseInto`, ns/op, 0 allocs, Ryzen 9 7950X, go1.27.1 windows/amd64,
`-cpu 1 -count 10`, medians from `benchstat`:

| Input                                          | Before | Pure Go | AVX2 | AVX-512 |
| ---------------------------------------------- | -----: | ------: | ---: | ------: |
| `http://192.0.2.146:8080`                      |   35.7 |    12.3 | 12.3 |    12.3 |
| `http://proxy.example.com:8080`                |   30.4 |    12.4 | 12.4 |    12.4 |
| `socks5://alice:s3cr3t@203.0.113.27:1080`      |   54.2 |    21.1 | 25.1 |    22.1 |
| `socks5://alice:s3cr3t@proxy.example.com:1080` |   45.7 |    19.7 | 18.2 |    15.1 |
| 92-byte residential line                       |   64.8 |    28.4 | 28.5 |    28.5 |
| 1024 mixed lines, varied shape                 |   60.8 |    24.4 | 22.7 |    20.6 |
| lenient, credentials absent                    |   51.6 |    35.0 | 29.2 |    28.9 |
| `(%t)%h:%d:%u:%p` custom delimiters            |   74.4 |    53.4 | 50.3 |    49.3 |
| `http://[2001:db8::1]:8080`                    |   31.5 |    35.4 | 35.4 |    35.4 |

An IPv6 literal and invalid input try the fast path first and then take the
byte-wise parser, which costs about 1 to 4 ns more than before, up to 30% on
the cheapest error paths. NEON is verified for
correctness on linux/arm64 under emulation; its speed is not measured here.
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
| `ModeSequential` | File order, bounded `bufio.Reader`                |
| `ModeShuffled`   | Locality-preserving region/block/line permutation |

Shuffled mode uses bounded working memory and no sidecar index. It opens in
constant time and loads blocks lazily. Order is not a uniform global line
permutation. `MaxLineBytes` bounds returned line size; oversized lines return
`ErrLineTooLong`.

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

Configure shuffled pools with `Open`:

```go
pool, err := proxypool.Open("proxies.txt", proxypool.Options{
	Mode:         proxypool.ModeShuffled,
	Reuse:        true,
	BlockBytes:   4 << 20,
	RegionBytes:  1 << 30,
	MaxLineBytes: 64 << 10,
	Seed:         12345,
})
```

`Stats` reports file size, blocks, regions, cursor, cycle, seed, retained
capacity, limits, shard configuration, and closed state. `Reset` restores cycle
zero and clears terminal read errors after source validation. Size and
modification-time checks run at bounded checkpoints. Non-EOF errors remain
terminal until `Reset` succeeds.

For parallel storage reads, use pools with matching source, `BlockBytes`,
`RegionBytes`, nonzero `Seed`, and `ShardCount`, assigning each
`ShardIndex` in `[0, ShardCount)`. Shards collectively return every line once.
`Reuse` cannot be combined with sharding.

## License

Licensed under the [MIT License](LICENSE).
