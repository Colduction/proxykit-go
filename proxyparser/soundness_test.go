package proxyparser_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
	"github.com/colduction/proxykit-go/proxyparser"
)

const (
	formatSchemeHostPort  = "%t://%h:%d"
	formatFullCredentials = "%t://%u:%p@%h:%d"
)

func availableBackends() []structuralindex.Backend {
	// SetBackend ignores a backend the processor lacks, so probing each one
	// yields the set this machine runs.
	previous := structuralindex.ActiveBackend()
	defer structuralindex.SetBackend(previous)
	var backends []structuralindex.Backend
	for _, backend := range []structuralindex.Backend{structuralindex.Portable, structuralindex.AVX2, structuralindex.AVX512, structuralindex.NEON} {
		structuralindex.SetBackend(backend)
		if structuralindex.ActiveBackend() == backend {
			backends = append(backends, backend)
		}
	}
	return backends
}

func diffEntryPoints(parser *proxyparser.Parse, input string) string {
	// The result describes the first entry point whose result differs from
	// the scalar parser, and is empty when none does.
	sentinel := proxykit.Proxy{Scheme: "x", Host: "x", Username: "x", Password: "x"}
	want := sentinel
	wantErr := parser.ParseIntoScalar(input, &want)

	got := sentinel
	gotErr := parser.ParseInto(input, &got)
	if got != want || !sameParseError(gotErr, wantErr) {
		return fmt.Sprintf("ParseInto(%q) = %+v, %v; scalar %+v, %v", input, got, gotErr, want, wantErr)
	}
	value, err := parser.ParseString(input)
	if value != want || !sameParseError(err, wantErr) {
		return fmt.Sprintf("ParseString(%q) = %+v, %v; scalar %+v, %v", input, value, err, want, wantErr)
	}
	got = sentinel
	// The copy has exact capacity, so a read past the end of the input is
	// more likely to reach memory that the checker or a guard notices.
	raw := make([]byte, len(input))
	copy(raw, input)
	gotErr = parser.ParseBytes(raw, &got)
	if got != want || !sameParseError(gotErr, wantErr) {
		return fmt.Sprintf("ParseBytes(%q) = %+v, %v; scalar %+v, %v", input, got, gotErr, want, wantErr)
	}
	pointer, err := parser.Parse(input)
	if !sameParseError(err, wantErr) {
		return fmt.Sprintf("Parse(%q) error = %v; scalar %v", input, err, wantErr)
	}
	if (pointer == nil) != (wantErr != nil) || (pointer != nil && *pointer != want) {
		return fmt.Sprintf("Parse(%q) = %+v; scalar %+v, %v", input, pointer, want, wantErr)
	}
	if wantErr == nil && !want.IsValid() {
		return fmt.Sprintf("scalar accepted %q as %+v, which Validate rejects", input, want)
	}
	return ""
}

func forEachBackend(t *testing.T, run func(t *testing.T)) {
	eachBackend(t, func(backend structuralindex.Backend) {
		t.Run(backend.String(), run)
	})
}

func enumerate(alphabet string, maxLen int, visit func(s string)) {
	buf := make([]byte, maxLen)
	var walk func(depth, length int)
	walk = func(depth, length int) {
		if depth == length {
			visit(string(buf[:length]))
			return
		}
		for i := range len(alphabet) {
			buf[depth] = alphabet[i]
			walk(depth+1, length)
		}
	}
	for length := 0; length <= maxLen; length++ {
		walk(0, length)
	}
}

func TestSoundnessHostPortExhaustive(t *testing.T) {
	maxLen := 8
	if testing.Short() {
		maxLen = 6
	}
	forEachBackend(t, func(t *testing.T) {
		var total, valid, fastPlain, fastIndexed, failures int
		enumerate("a01256-.:", maxLen, func(s string) {
			total++
			want := proxykit.IsValidHostnamePort(s)
			if want {
				valid++
			}
			for _, indexed := range []bool{false, true} {
				if !proxyparser.IsHostPortFast(s, indexed) {
					continue
				}
				if indexed {
					fastIndexed++
				} else {
					fastPlain++
				}
				if !want && failures < 20 {
					failures++
					t.Errorf("IsHostPortFast(%q, %v) accepts what IsValidHostnamePort rejects", s, indexed)
				}
			}
		})
		t.Logf("strings %d, valid %d, fast plain %d, fast indexed %d", total, valid, fastPlain, fastIndexed)
	})
}

func TestSoundnessHostPortPositions(t *testing.T) {
	// Every short pattern slides across every offset of a 64-byte text, so
	// label edges land on each word boundary and on positions 62 and 63.
	var patterns []string
	enumerate("aZ9-.:", 5, func(s string) { patterns = append(patterns, s) })
	full := mustNew(t, formatFullCredentials, true)
	plain := mustNew(t, formatSchemeHostPort, true)
	forEachBackend(t, func(t *testing.T) {
		var failures int
		fail := func(format string, args ...any) {
			if failures++; failures <= 20 {
				t.Errorf(format, args...)
			}
		}
		for pad := 0; pad <= 70; pad++ {
			prefix := strings.Repeat("a", pad)
			for _, fill := range []string{"", "b", "b:1", ":1", ":65535", ":65536"} {
				for _, pattern := range patterns {
					hostPort := prefix + pattern + fill
					want := proxykit.IsValidHostnamePort(hostPort)
					for _, indexed := range []bool{false, true} {
						if proxyparser.IsHostPortFast(hostPort, indexed) && !want {
							fail("IsHostPortFast(%q, %v) accepts an invalid host and port", hostPort, indexed)
						}
					}
				}
			}
		}
		for pad := 0; pad <= 60; pad += 1 {
			prefix := strings.Repeat("a", pad)
			for _, pattern := range patterns {
				if len(pattern) > 4 {
					continue
				}
				for _, input := range []string{
					"http://" + prefix + pattern + ":1",
					"socks5://u:p@" + prefix + pattern + ":1",
					"http://" + prefix + ":" + pattern + "@h.b:1",
					"socks4://" + pattern + ":" + prefix + "@h.b:1",
				} {
					if diff := diffEntryPoints(plain, input); diff != "" {
						fail("%s: %s", formatSchemeHostPort, diff)
					}
					if diff := diffEntryPoints(full, input); diff != "" {
						fail("%s: %s", formatFullCredentials, diff)
					}
				}
			}
		}
	})
}

func TestSoundnessEveryByteEveryPosition(t *testing.T) {
	bases := []string{
		"http://proxy.example.com:8080",
		"https://user:pass@proxy.example.com:8080",
		"socks5h://user:pa:ss@10.0.0.1:1080",
		"socks4a://user:@a-b.c:1",
		"socks4://user:pass@a-b.c:1",
		"http://:pass@h:1",
		"http://:@h:1",
		"http://u:p@" + strings.Repeat("a", 49) + ".b:1",
		"http://u:p@" + strings.Repeat("a", 50) + ".b:1",
		"http://u:p@" + strings.Repeat("a", 51) + ".b:1",
		"http://" + strings.Repeat("a", 63) + ".b:1",
		"http://" + strings.Repeat("a", 64) + ".b:1",
	}
	parsers := []*proxyparser.Parse{mustNew(t, formatSchemeHostPort, true), mustNew(t, formatFullCredentials, true)}
	forEachBackend(t, func(t *testing.T) {
		var failures int
		for _, base := range bases {
			for position := 0; position <= len(base); position++ {
				for value := range 256 {
					variants := []string{base[:position] + string([]byte{byte(value)}) + base[position:]}
					if position < len(base) {
						variants = append(variants, base[:position]+string([]byte{byte(value)})+base[position+1:])
					}
					for _, input := range variants {
						for _, parser := range parsers {
							if diff := diffEntryPoints(parser, input); diff != "" {
								if failures++; failures <= 20 {
									t.Error(diff)
								}
							}
						}
					}
				}
			}
		}
	})
}

func soundnessSeeds() []string {
	seeds := []string{
		"", "h", "http", "http://", "http://a", "http://a:", "http://a:1", "http://:1", "http://:",
		"HTTP://a:1", "Http://a.b:80", "SOCKS5://u:p@a.b:80", "socks4://u:p@a.b:1", "socks4a://u:p@a.b:1",
		"socks4://u:@a.b:1", "socks4a://u:@a.b:1", "socks4://:@a.b:1", "socks5://:@a.b:1",
		"http://:pass@h:1", "http://u:p@h:1", "http://u:p@@h:1", "http://u:p@x@h:1", "http://u:p:q@h:1",
		"http://u@v:p@h:1", "http://u:p@h:1@h:2", "http://u:p@[::1]:80", "http://[::1]:80", "http://[::1%25eth0]:80",
		"http://u:p@h", "http://u:p@:1", "http://u:p@h:", "http://u:p@h:0", "http://u:p@h:00080",
		"http://h:0", "http://h:00080", "http://h:000080", "http://h:65535", "http://h:65536", "http://h:99999",
		"http://h:100000", "http://h:4294967297", "http://h:00000", "http://h:-1", "http://h:+1", "http://h: 1",
		"http://0.0.0.0:1", "http://255.255.255.255:1", "http://256.1.1.1:1", "http://01.2.3.4:1",
		"http://1.2.3:1", "http://1.2.3.4.5:1", "http://1.2.3.4.:1", "http://.1.2.3.4:1", "http://1..2.3:1",
		"http://a1:1", "http://a.1:1", "http://1a:1", "http://a-1:1", "http://1-1:1", "http://a.b.:1", "http://.:1",
		"http://-a:1", "http://a-:1", "http://a.-b:1", "http://a-.b:1", "http://a..b:1", "http://a_b:1",
		"http://a\x00b:1", "http://a\x7fb:1", "http://a\x80b:1", "http://a\xffb:1", "http://\xe2\x80\xa8:1",
		"http://u\x00:p@h:1", "http://u:p\x7f@h:1", "http://u:p\x80@h:1", "http://u :p ~@h:1", "http://u\x1f:p@h:1",
		"http:///a:1", "http:/a:1", "http//a:1", "https:/a.b:1", "https//a.b:1", "httpss://a:1",
		"socks://a:1", "socks5://a:1", "socks5h://a:1", "socks4a://a:1", "socks4://a:1", "socks5a://a:1", "socks4h://a:1",
		"socks6://a:1", "socks5h:/a:12", "socks4a:/a:12", "sOcks5://a:1", "socks5H://a:1", "http://a://b:1",
		"x://http://a:1", "://a:1", "http://http://a:1", "http://u:p@http://a:1",
	}
	for _, size := range []int{50, 51, 52, 53, 54, 55, 56, 57, 60, 61, 62, 63, 64, 65, 250, 253, 254, 255, 256} {
		name := strings.Repeat("a", size)
		seeds = append(seeds,
			"http://"+name+":1", "http://"+name+".b:1", "http://b."+name+":1", "http://"+name+"-:1", "http://"+name+".:1",
			"http://u:p@"+name+":1", "http://u:p@"+name+".:1", "http://u:p@"+name+"-:1", "http://u:p@"+name+".b:1",
			"http://"+name+":p@h:1", "http://u:"+name+"@h:1", "socks5://"+name+":"+name+"@h.b:1",
		)
	}
	return seeds
}

func TestSoundnessSeeds(t *testing.T) {
	parsers := []*proxyparser.Parse{mustNew(t, formatSchemeHostPort, true), mustNew(t, formatFullCredentials, true)}
	forEachBackend(t, func(t *testing.T) {
		for _, seed := range soundnessSeeds() {
			for _, parser := range parsers {
				if diff := diffEntryPoints(parser, seed); diff != "" {
					t.Error(diff)
				}
			}
		}
	})
}

func fuzzFormat(f *testing.F, format string) {
	for _, seed := range soundnessSeeds() {
		f.Add(seed)
	}
	parser := mustNew(f, format, true)
	backends := availableBackends()
	f.Fuzz(func(t *testing.T, input string) {
		previous := structuralindex.ActiveBackend()
		defer structuralindex.SetBackend(previous)
		for _, backend := range backends {
			structuralindex.SetBackend(backend)
			if diff := diffEntryPoints(parser, input); diff != "" {
				t.Fatalf("%v: %s", backend, diff)
			}
		}
	})
}

func FuzzSoundnessSchemeHostPort(f *testing.F) {
	fuzzFormat(f, formatSchemeHostPort)
}

func FuzzSoundnessFullCredentials(f *testing.F) {
	fuzzFormat(f, formatFullCredentials)
}

func FuzzSoundnessAssembled(f *testing.F) {
	// Assembling the input from parts keeps the fuzzer near the accepted
	// language, where a wrong acceptance would hide.
	f.Add(uint8(0), "user", "pass", "proxy.example.com", "8080")
	f.Add(uint8(3), "user", "", "10.0.0.1", "1080")
	f.Add(uint8(5), "", "", "a-b.c", "00001")
	f.Add(uint8(2), "u", "p@q", "a.b1", "65535")
	f.Add(uint8(7), "u", "p", strings.Repeat("a", 49)+".b", "1")
	schemes := []string{"http", "https", "socks4", "socks4a", "socks5", "socks5h", "HTTP", "socks", "socks5a", "Socks4a"}
	plain, full := mustNew(f, formatSchemeHostPort, true), mustNew(f, formatFullCredentials, true)
	backends := availableBackends()
	f.Fuzz(func(t *testing.T, selector uint8, username, password, host, port string) {
		previous := structuralindex.ActiveBackend()
		defer structuralindex.SetBackend(previous)
		scheme := schemes[int(selector)%len(schemes)]
		inputs := []string{
			scheme + "://" + host + ":" + port,
			scheme + "://" + username + ":" + password + "@" + host + ":" + port,
		}
		for _, backend := range backends {
			structuralindex.SetBackend(backend)
			for _, input := range inputs {
				if diff := diffEntryPoints(plain, input); diff != "" {
					t.Fatalf("%v %s: %s", backend, formatSchemeHostPort, diff)
				}
				if diff := diffEntryPoints(full, input); diff != "" {
					t.Fatalf("%v %s: %s", backend, formatFullCredentials, diff)
				}
			}
			for _, indexed := range []bool{false, true} {
				hostPort := host + ":" + port
				if proxyparser.IsHostPortFast(hostPort, indexed) && !proxykit.IsValidHostnamePort(hostPort) {
					t.Fatalf("%v: IsHostPortFast(%q, %v) accepts an invalid host and port", backend, hostPort, indexed)
				}
			}
		}
	})
}
