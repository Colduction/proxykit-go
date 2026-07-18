package proxyparser_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/proxyparser"
)

func mustNew(t *testing.T, format string, strict bool) *proxyparser.Parse {
	t.Helper()
	p, err := proxyparser.New(format, strict)
	if err != nil {
		t.Fatalf("New(%q, %v) unexpected error: %v", format, strict, err)
	}
	return p
}

func TestNew_InvalidFormatVerb(t *testing.T) {
	_, err := proxyparser.New("%t://%x", false)
	if err == nil {
		t.Fatal("expected error for unknown verb x, got nil")
	}
	if _, ok := errors.AsType[proxyparser.ErrInvalidFormatVerb](err); !ok {
		t.Fatalf("expected ErrInvalidFormatVerb, got %T: %v", err, err)
	}
}

func TestNew_FormatEndingWithPercent(t *testing.T) {
	_, err := proxyparser.New("%t://%h:%", false)
	if err == nil {
		t.Fatal("expected error for format ending with '%', got nil")
	}
	if !errors.Is(err, proxyparser.ErrInvalidFormatEndStr) {
		t.Fatalf("expected ErrInvalidFormatEndStr, got %T: %v", err, err)
	}
}

func TestNew_EscapedPercent(t *testing.T) {
	p, err := proxyparser.New("%%%t://%h:%d", true)
	if err != nil {
		t.Fatalf("New with escaped %% failed: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil parser")
	}
}

func TestNew_ValidFormats(t *testing.T) {
	formats := []string{
		"%t://%h",
		"%t://%h:%d",
		"%t://%u:%p@%h:%d",
		"(%t)%h:%d:%u:%p",
		"%t://%u@%h:%d",
	}
	for _, f := range formats {
		_, err := proxyparser.New(f, false)
		if err != nil {
			t.Errorf("New(%q) unexpected error: %v", f, err)
		}
	}
}

func TestParse_SchemeAndHostPort_Strict(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	proxy, err := p.Parse("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTP, proxy.Scheme)
	}
	if proxy.Host != "proxy.example.com:8080" {
		t.Errorf("host: want %q, got %q", "proxy.example.com:8080", proxy.Host)
	}
}

func TestParse_FullFormat_Strict(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", true)
	proxy, err := p.Parse("socks5://alice:s3cr3t@proxy.example.com:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.SOCKS5 {
		t.Errorf("scheme: want %q, got %q", proxykit.SOCKS5, proxy.Scheme)
	}
	if proxy.Host != "proxy.example.com:1080" {
		t.Errorf("host: want %q, got %q", "proxy.example.com:1080", proxy.Host)
	}
	if proxy.Username != "alice" {
		t.Errorf("username: want %q, got %q", "alice", proxy.Username)
	}
	if proxy.Password != "s3cr3t" {
		t.Errorf("password: want %q, got %q", "s3cr3t", proxy.Password)
	}
}

func TestParse_CustomDelimiters_Lenient(t *testing.T) {
	p := mustNew(t, "(%t)%h:%d:%u:%p", false)
	proxy, err := p.Parse("(http)res-us.lightningproxies.net:9999:admin:pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTP, proxy.Scheme)
	}
	if proxy.Host != "res-us.lightningproxies.net:9999" {
		t.Errorf("host: want %q, got %q", "res-us.lightningproxies.net:9999", proxy.Host)
	}
	if proxy.Username != "admin" {
		t.Errorf("username: want %q, got %q", "admin", proxy.Username)
	}
	if proxy.Password != "pass" {
		t.Errorf("password: want %q, got %q", "pass", proxy.Password)
	}
}

func TestParse_Lenient_NoSchemeInFormat_AutoDetect(t *testing.T) {
	p := mustNew(t, "%h:%d", false)
	proxy, err := p.Parse("http://proxy.example.com:3128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTP, proxy.Scheme)
	}
	if proxy.Host != "proxy.example.com:3128" {
		t.Errorf("host: want %q, got %q", "proxy.example.com:3128", proxy.Host)
	}
}

func TestParse_Lenient_MissingCredentials(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", false)
	proxy, err := p.Parse("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTP, proxy.Scheme)
	}
	if proxy.Host != "proxy.example.com:8080" {
		t.Errorf("host: want %q, got %q", "proxy.example.com:8080", proxy.Host)
	}
	if proxy.Username != "" {
		t.Errorf("username: want empty, got %q", proxy.Username)
	}
}

func TestParse_Lenient_OnlyUserNoPassword(t *testing.T) {
	p := mustNew(t, "%t://%u@%h:%d", false)
	proxy, err := p.Parse("http://bob@proxy.example.com:3128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Username != "bob" {
		t.Errorf("username: want %q, got %q", "bob", proxy.Username)
	}
	if proxy.Host != "proxy.example.com:3128" {
		t.Errorf("host: want %q, got %q", "proxy.example.com:3128", proxy.Host)
	}
}

func TestParse_HTTPS(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	proxy, err := p.Parse("https://secure.proxy.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTPS {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTPS, proxy.Scheme)
	}
}

func TestParse_SOCKS5H(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	proxy, err := p.Parse("socks5h://proxy.example.com:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.SOCKS5H {
		t.Errorf("scheme: want %q, got %q", proxykit.SOCKS5H, proxy.Scheme)
	}
}

func TestParse_EscapedPercent(t *testing.T) {
	p := mustNew(t, "%%%t://%h:%d", true)
	proxy, err := p.Parse("%http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP {
		t.Errorf("scheme: want %q, got %q", proxykit.HTTP, proxy.Scheme)
	}
}

func TestParse_MissingScheme_Lenient(t *testing.T) {
	p := mustNew(t, "%h:%d", false)
	_, err := p.Parse("proxy.example.com:8080")
	if err == nil {
		t.Fatal("expected ErrSchemeNotParsed, got nil")
	}
	if !errors.Is(err, proxyparser.ErrSchemeNotParsed) {
		t.Fatalf("expected ErrSchemeNotParsed, got %T: %v", err, err)
	}
}

func TestParse_InvalidProxyFormat(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	_, err := p.Parse("badscheme://proxy.example.com:8080")
	if err == nil {
		t.Fatal("expected error for invalid scheme, got nil")
	}
	if !errors.Is(err, proxyparser.ErrInvalidProxyFormat) {
		t.Fatalf("expected ErrInvalidProxyFormat, got %T: %v", err, err)
	}
}

func TestParse_StrictMode_TrailingChars(t *testing.T) {
	p := mustNew(t, "%t://%h:%d!", true)
	_, err := p.Parse("http://proxy.example.com:8080!extra")
	if err == nil {
		t.Fatal("expected ErrUnexpectedTrail, got nil")
	}
	if _, ok := errors.AsType[proxyparser.ErrUnexpectedTrail](err); !ok {
		t.Fatalf("expected ErrUnexpectedTrail, got %T: %v", err, err)
	}
}

func TestParse_StrictMode_MismatchDelimiter(t *testing.T) {
	p := mustNew(t, "(%t)%h:%d", true)
	_, err := p.Parse("http)proxy.example.com:8080")
	if err == nil {
		t.Fatal("expected ErrMismatchDelim, got nil")
	}
	if _, ok := errors.AsType[*proxyparser.ErrMismatchDelim](err); !ok {
		t.Fatalf("expected ErrMismatchDelim, got %T: %v", err, err)
	}
}

func TestParse_NilPlan(t *testing.T) {
	p, err := proxyparser.New("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	proxy, err := p.Parse("http://proxy.example.com:8080")
	if proxy != nil {
		t.Fatalf("proxy: got %+v, want nil", proxy)
	}
	if !errors.Is(err, proxyparser.ErrHostNotParsed) {
		t.Fatalf("error: got %v, want ErrHostNotParsed", err)
	}
}

func TestParse_SubseqDelimNotFound_Strict(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", true)
	_, err := p.Parse("http://user:passNOAT-proxy.example.com:8080")
	if missing, ok := errors.AsType[proxyparser.ErrSubseqDelimNotFound](err); !ok || missing != "@" {
		t.Fatalf("error: got %v, want missing @ delimiter", err)
	}
}

func TestParse_LenientDelimiterMismatchCompatibility(t *testing.T) {
	p := mustNew(t, "(%t)%h:%d", false)
	proxy, err := p.ParseString("http)proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Scheme != proxykit.HTTP || proxy.Host != "proxy.example.com:8080" {
		t.Fatalf("proxy: got %+v", proxy)
	}
}

func TestParse_LenientMissingPortCompatibility(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", false)
	_, err := p.ParseString("http://proxy.example.com")
	if !errors.Is(err, proxyparser.ErrInvalidProxyFormat) {
		t.Fatalf("error: got %v, want ErrInvalidProxyFormat", err)
	}
}

func TestParse_StrictMissingPortDelimiter(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	_, err := p.ParseString("http://proxy.example.com")
	if missing, ok := errors.AsType[proxyparser.ErrSubseqDelimNotFound](err); !ok || missing != ":" {
		t.Fatalf("error: got %v, want missing : delimiter", err)
	}
}

func TestCanonicalErrorResults(t *testing.T) {
	tests := []struct {
		format, input string
		want          proxykit.Proxy
		wantErr       error
	}{
		{"%t://%h:%d", "", proxykit.Proxy{}, proxyparser.ErrSubseqDelimNotFound("://")},
		{"%t://%h:%d", "http://host", proxykit.Proxy{}, proxyparser.ErrSubseqDelimNotFound(":")},
		{"%t://%h:%d", "ftp://host:80", proxykit.Proxy{Scheme: "ftp", Host: "host:80"}, proxyparser.ErrInvalidProxyFormat},
		{"%t://%h:%d", "http://bad host:80", proxykit.Proxy{Scheme: proxykit.HTTP, Host: "bad host:80"}, proxyparser.ErrInvalidProxyFormat},
		{"%t://%u:%p@%h:%d", "http://user", proxykit.Proxy{}, proxyparser.ErrSubseqDelimNotFound(":")},
		{"%t://%u:%p@%h:%d", "http://user:pass-host:80", proxykit.Proxy{}, proxyparser.ErrSubseqDelimNotFound("@")},
		{"%t://%u:%p@%h:%d", "http://user:pass@host", proxykit.Proxy{}, proxyparser.ErrSubseqDelimNotFound(":")},
		{"%t://%u:%p@%h:%d", "http://:pass@host:80", proxykit.Proxy{Scheme: proxykit.HTTP, Host: "host:80", Password: "pass"}, proxyparser.ErrInvalidProxyFormat},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			parser := mustNew(t, tt.format, true)
			parsed, err := parser.ParseString(tt.input)
			if parsed != tt.want || !sameParseError(err, tt.wantErr) {
				t.Fatalf("ParseString: got (%+v, %v), want (%+v, %v)", parsed, err, tt.want, tt.wantErr)
			}

			into := proxykit.Proxy{Scheme: proxykit.SOCKS5H, Host: "stale:1", Username: "stale", Password: "stale"}
			intoErr := parser.ParseInto(tt.input, &into)
			if into != tt.want || !sameParseError(intoErr, tt.wantErr) {
				t.Fatalf("ParseInto: got (%+v, %v), want (%+v, %v)", into, intoErr, tt.want, tt.wantErr)
			}

			var fromBytes proxykit.Proxy
			bytesErr := parser.ParseBytes([]byte(tt.input), &fromBytes)
			if fromBytes != tt.want || !sameParseError(bytesErr, tt.wantErr) {
				t.Fatalf("ParseBytes: got (%+v, %v), want (%+v, %v)", fromBytes, bytesErr, tt.want, tt.wantErr)
			}

			pointer, pointerErr := parser.Parse(tt.input)
			if pointer != nil || !sameParseError(pointerErr, tt.wantErr) {
				t.Fatalf("Parse: got (%+v, %v), want (nil, %v)", pointer, pointerErr, tt.wantErr)
			}
		})
	}
}

func TestGenericDelimiterOwnership(t *testing.T) {
	tests := []struct {
		name, format, input string
		strict              bool
		want                proxykit.Proxy
		wantErr             string
	}{
		{"escaped percent", "%t%%/%h:%d", "http%/host:80", true, proxykit.Proxy{Scheme: proxykit.HTTP, Host: "host:80"}, ""},
		{"strict mismatch", "%t%%/%h:%d", "http%Xhost:80", true, proxykit.Proxy{Scheme: proxykit.HTTP}, `proxyparser: expected delimiter "/", but mismatch found at position 5`},
		{"lenient mismatch", "%t%%/%h:%d", "http%Xhost:80", false, proxykit.Proxy{Scheme: proxykit.HTTP, Host: "Xhost:80"}, ""},
		{"multi-byte", "%t::%h:%d", "http::host:80", true, proxykit.Proxy{Scheme: proxykit.HTTP, Host: "host:80"}, ""},
		{"port normalization", "%t://%h|%d", "http://host|80", true, proxykit.Proxy{Scheme: proxykit.HTTP, Host: "host:80"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := mustNew(t, tt.format, tt.strict).ParseString(tt.input)
			if parsed != tt.want {
				t.Fatalf("proxy: got %+v, want %+v", parsed, tt.want)
			}
			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tt.wantErr {
				t.Fatalf("error: got %q, want %q", gotErr, tt.wantErr)
			}
		})
	}
}

func TestParse_Idempotent(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", true)
	input := "socks5://user:pass@proxy.example.com:1080"
	p1, err1 := p.Parse(input)
	p2, err2 := p.Parse(input)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if *p1 != *p2 {
		t.Errorf("idempotency: first=%+v, second=%+v", p1, p2)
	}
}

func TestParseInto_ZeroAlloc(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", true)
	input := "socks5://user:pass@proxy.example.com:1080"
	var proxy proxykit.Proxy
	allocs := testing.AllocsPerRun(1_000, func() {
		if err := p.ParseInto(input, &proxy); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("got %.2f allocs, want 0", allocs)
	}
	if proxy.Host != "proxy.example.com:1080" {
		t.Fatalf("host: got %q", proxy.Host)
	}
}

func TestParseInto_NilDestination(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	if err := p.ParseInto("http://proxy.example.com:8080", nil); !errors.Is(err, proxyparser.ErrNilProxy) {
		t.Fatalf("got %v, want ErrNilProxy", err)
	}
}

func TestParseString_ZeroAlloc(t *testing.T) {
	p := mustNew(t, "%t://%u:%p@%h:%d", true)
	input := "socks5://user:pass@proxy.example.com:1080"
	allocs := testing.AllocsPerRun(1_000, func() {
		proxy, err := p.ParseString(input)
		if err != nil {
			panic(err)
		}
		if proxy.Host != "proxy.example.com:1080" {
			panic(proxy.Host)
		}
	})
	if allocs != 0 {
		t.Fatalf("got %.2f allocs, want 0", allocs)
	}
}

func TestParse_ErrorZeroAlloc(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	input := "invalid://proxy.example.com:8080"
	allocs := testing.AllocsPerRun(1_000, func() {
		proxy, err := p.Parse(input)
		if proxy != nil || !errors.Is(err, proxyparser.ErrInvalidProxyFormat) {
			panic("unexpected parse result")
		}
	})
	if allocs != 0 {
		t.Fatalf("got %.2f allocs, want 0", allocs)
	}
}

func TestParseBytes_ZeroAlloc(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	input := []byte("http://proxy.example.com:8080")
	var proxy proxykit.Proxy
	allocs := testing.AllocsPerRun(1_000, func() {
		if err := p.ParseBytes(input, &proxy); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("got %.2f allocs, want 0", allocs)
	}
	if proxy.Host != "proxy.example.com:8080" {
		t.Fatalf("host: got %q", proxy.Host)
	}
}

func TestParse_StrictIPv6HostPort(t *testing.T) {
	p := mustNew(t, "%t://%h:%d", true)
	proxy, err := p.ParseString("http://[2001:db8::1]:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proxy.Host != "[2001:db8::1]:8080" {
		t.Fatalf("host: got %q", proxy.Host)
	}
}

func FuzzParseEntryPointParity(f *testing.F) {
	seeds := []struct {
		format string
		input  string
		strict bool
	}{
		{format: "%t://%h:%d", input: "http://proxy.example.com:8080", strict: true},
		{format: "%t://%u:%p@%h:%d", input: "socks5://user:pass@[2001:db8::1]:1080", strict: true},
		{format: "%t://%u:%p@%h:%d", input: "http://proxy.example.com:3128", strict: false},
		{format: "(%t)%h:%d:%u:%p", input: "(http)proxy.example.com:8080:user:pass", strict: false},
		{format: "%%%t://%h:%d", input: "%https://proxy.example.com:443", strict: true},
		{format: "", input: "http://proxy.example.com:8080", strict: false},
	}
	for _, seed := range seeds {
		f.Add(seed.format, seed.input, seed.strict)
	}

	f.Fuzz(func(t *testing.T, format, input string, strict bool) {
		parser, err := proxyparser.New(format, strict)
		if err != nil {
			return
		}

		parsed, parseErr := parser.ParseString(input)
		into := proxykit.Proxy{
			Scheme:   proxykit.SOCKS5H,
			Host:     "stale.example.com:1",
			Username: "stale-user",
			Password: "stale-password",
		}
		intoErr := parser.ParseInto(input, &into)
		var fromBytes proxykit.Proxy
		bytesErr := parser.ParseBytes([]byte(input), &fromBytes)

		if !sameParseError(parseErr, intoErr) || !sameParseError(parseErr, bytesErr) {
			t.Fatalf("errors differ: ParseString=%v ParseInto=%v ParseBytes=%v", parseErr, intoErr, bytesErr)
		}
		if parsed != into || parsed != fromBytes {
			t.Fatalf("results differ: ParseString=%+v ParseInto=%+v ParseBytes=%+v", parsed, into, fromBytes)
		}

		pointer, pointerErr := parser.Parse(input)
		if !sameParseError(parseErr, pointerErr) {
			t.Fatalf("Parse error differs: ParseString=%v Parse=%v", parseErr, pointerErr)
		}
		if pointerErr != nil {
			if pointer != nil {
				t.Fatalf("Parse returned proxy on error: %+v", pointer)
			}
			return
		}
		if pointer == nil || *pointer != parsed {
			t.Fatalf("Parse result differs: ParseString=%+v Parse=%+v", parsed, pointer)
		}
	})
}

func sameParseError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.TypeOf(left) == reflect.TypeOf(right) && left.Error() == right.Error()
}

func BenchmarkParse_SchemeHostPort(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "http://proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_FullCredentials(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", true)
	input := "socks5://alice:s3cr3t@proxy.example.com:1080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParseInto_FullCredentials(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", true)
	input := "socks5://alice:s3cr3t@proxy.example.com:1080"
	var proxy proxykit.Proxy
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ParseInto(input, &proxy)
	}
}

func BenchmarkParseString_FullCredentials(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", true)
	input := "socks5://alice:s3cr3t@proxy.example.com:1080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.ParseString(input)
	}
}

func BenchmarkParseBytes_SchemeHostPort(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := []byte("http://proxy.example.com:8080")
	var proxy proxykit.Proxy
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ParseBytes(input, &proxy)
	}
}

func BenchmarkParse_CustomDelimiters(b *testing.B) {
	p, _ := proxyparser.New("(%t)%h:%d:%u:%p", false)
	input := "(http)res-us.lightningproxies.net:9999:admin:pass"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParseInto_CustomDelimiters(b *testing.B) {
	p, _ := proxyparser.New("(%t)%h:%d:%u:%p", false)
	input := "(http)res-us.lightningproxies.net:9999:admin:pass"
	var proxy proxykit.Proxy
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ParseInto(input, &proxy)
	}
}

func BenchmarkParse_Lenient_NoCredentials(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", false)
	input := "http://proxy.example.com:3128"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParseInto_Lenient_NoCredentials(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", false)
	input := "http://proxy.example.com:3128"
	var proxy proxykit.Proxy
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ParseInto(input, &proxy)
	}
}

func BenchmarkParse_AutoScheme(b *testing.B) {
	p, _ := proxyparser.New("%h:%d", false)
	input := "http://proxy.example.com:3128"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_Lenient_OnlyUserNoPassword(b *testing.B) {
	p, _ := proxyparser.New("%t://%u@%h:%d", false)
	input := "http://bob@proxy.example.com:3128"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_HTTPS(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "https://secure.proxy.com:443"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_SOCKS5H(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "socks5h://proxy.example.com:1080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_EscapedPercent(b *testing.B) {
	p, _ := proxyparser.New("%%%t://%h:%d", true)
	input := "%http://proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_StrictIPv6HostPort(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "http://[2001:db8::1]:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.ParseString(input)
	}
}

func BenchmarkParse_MissingScheme_Lenient(b *testing.B) {
	p, _ := proxyparser.New("%h:%d", false)
	input := "proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_InvalidProxyFormat(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "badscheme://proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_StrictMode_TrailingChars(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d!", true)
	input := "http://proxy.example.com:8080!extra"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_StrictMode_MismatchDelimiter(b *testing.B) {
	p, _ := proxyparser.New("(%t)%h:%d", true)
	input := "http)proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_SubseqDelimNotFound_Strict(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", true)
	input := "http://user:passNOAT-proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
	}
}

func BenchmarkParse_NilPlan(b *testing.B) {
	p, _ := proxyparser.New("", false)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse("http://proxy.example.com:8080")
	}
}

func BenchmarkParse_Idempotent(b *testing.B) {
	p, _ := proxyparser.New("%t://%u:%p@%h:%d", true)
	input := "socks5://user:pass@proxy.example.com:1080"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Parse(input)
		_, _ = p.Parse(input)
	}
}

func BenchmarkParseInto_NilDestination(b *testing.B) {
	p, _ := proxyparser.New("%t://%h:%d", true)
	input := "http://proxy.example.com:8080"
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ParseInto(input, nil)
	}
}

func BenchmarkNew_InvalidFormatVerb(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = proxyparser.New("%t://%x", false)
	}
}

func BenchmarkNew_FormatEndingWithPercent(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = proxyparser.New("%t://%h:%", false)
	}
}

func BenchmarkNew_EscapedPercent(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = proxyparser.New("%%%t://%h:%d", true)
	}
}

func BenchmarkNew_ValidFormats(b *testing.B) {
	formats := []string{
		"%t://%h",
		"%t://%h:%d",
		"%t://%u:%p@%h:%d",
		"(%t)%h:%d:%u:%p",
		"%t://%u@%h:%d",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, format := range formats {
			_, _ = proxyparser.New(format, false)
		}
	}
}

func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = proxyparser.New("%t://%u:%p@%h:%d", true)
	}
}

func BenchmarkNew_LongFormat(b *testing.B) {
	format := strings.Repeat("%h:", 256) + "%d"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = proxyparser.New(format, false)
	}
}
