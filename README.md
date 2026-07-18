<div align="center">

# proxykit-go

**Fast, allocation-aware Go tools for HTTP and SOCKS proxies.**

[![Go Reference](https://pkg.go.dev/badge/github.com/colduction/proxykit-go.svg)](https://pkg.go.dev/github.com/colduction/proxykit-go)
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
> Requires Go 1.26 or later.

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
		fmt.Println(proxy.ExportURL())
	}
}
```

Supported schemes: `http`, `https`, `socks5`, `socks5h`.

> [!IMPORTANT]
> Use `host:port` for DNS/IPv4 and `[ipv6]:port` for IPv6.

Host validation accepts ASCII DNS names, IPv4, and IPv6. DNS labels follow LDH
rules, with 63-byte labels and DNS presentation-length limits. Root-dot names
and IPv6 zone IDs are supported.

Helpers include `IsValidScheme`, `IsValidHost`, `IsValidHostnamePort`,
`IsValidCredentials`, and `SplitHostnamePort`.

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
optional credentials or ports after parsing scheme and host. Use `ParseString`
for a value result, `ParseInto` for caller-owned reuse, and `ParseBytes` for
zero-copy input.

> [!WARNING]
> `ParseBytes` aliases its input. Keep input immutable while parsed fields remain
> in use; concurrent calls need distinct destinations.

Use `errors.Is` for sentinels such as `ErrInvalidProxyFormat`, and
`errors.As`/`errors.AsType` for typed parse errors.

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
