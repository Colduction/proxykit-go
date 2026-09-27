package proxyparser_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/proxyparser"
)

// TestNewInvalidFormatAllocation checks that rejecting a format needs no parse plan.
func TestNewInvalidFormatAllocation(t *testing.T) {
	for _, test := range []struct {
		format string
		want   error
	}{
		{"%x" + strings.Repeat("%h:", 4096), proxyparser.ErrInvalidFormatVerb('x')},
		{strings.Repeat("%h:", 4096) + "%", proxyparser.ErrInvalidFormatEndStr},
	} {
		var err error
		allocs := testing.AllocsPerRun(10, func() {
			_, err = proxyparser.New(test.format, false)
		})
		if !errors.Is(err, test.want) {
			t.Fatalf("New error = %v, want %v", err, test.want)
		}
		if allocs != 0 {
			t.Errorf("New invalid format allocs = %v, want 0", allocs)
		}
	}
}

// TestParseLongPorts checks scalar fallback for long digit sequences.
func TestParseLongPorts(t *testing.T) {
	for _, port := range []string{strings.Repeat("9", 65536), strings.Repeat("0", 65536), strings.Repeat("0", 65536) + "80"} {
		for _, test := range []struct {
			format, prefix string
		}{
			{"%t://%h:%d", "http://host:"},
			{"%t://%u:%p@%h:%d", "http://u:p@host:"},
		} {
			parser := mustNew(t, test.format, true)
			if diff := diffEntryPoints(parser, test.prefix+port); diff != "" {
				t.Fatal(diff)
			}
			_, err := parser.ParseString(test.prefix + port)
			if strings.HasSuffix(port, "80") {
				if err != nil {
					t.Fatalf("leading-zero port: %v", err)
				}
			} else if !errors.Is(err, proxykit.ErrInvalidPort) {
				t.Fatalf("invalid long port error = %v, want ErrInvalidPort", err)
			}
		}
	}
}

// TestParseStrictEmptyEndpointFields rejects empty explicit endpoint captures.
func TestParseStrictEmptyEndpointFields(t *testing.T) {
	for _, test := range []struct {
		format, input string
		want          error
	}{
		{"%t://%h|%d", "http://host:80|", proxykit.ErrInvalidPort},
		{"%t://%h|%d", "http://|host:80", proxykit.ErrInvalidHost},
		{"%t://%h|%d!", "http://host:80|!", proxykit.ErrInvalidPort},
		{"%t://%h|%d!", "http://|host:80!", proxykit.ErrInvalidHost},
		{"%t://%h|%d", "ftp://host:80|", proxykit.ErrInvalidScheme},
		{"%t://%h|%d", "http://|", proxykit.ErrInvalidHost},
		{"%t://%h|%d", "http://host| ", proxykit.ErrInvalidPort},
		{"%t://%h|%d", "http:// |80", proxykit.ErrInvalidHost},
		{"%h|%d|%t", "host:80||ftp", proxykit.ErrInvalidScheme},
	} {
		parser := mustNew(t, test.format, true)
		_, err := parser.ParseString(test.input)
		if !errors.Is(err, proxyparser.ErrInvalidProxyFormat) || !errors.Is(err, test.want) {
			t.Errorf("ParseString(%q, %q) error = %v, want ErrInvalidProxyFormat wrapping %v", test.format, test.input, err, test.want)
		}
		if diff := diffEntryPoints(parser, test.input); diff != "" {
			t.Error(diff)
		}
	}
}

// TestParseDuplicateEndpointFields preserves replacement and lenient empty captures.
func TestParseDuplicateEndpointFields(t *testing.T) {
	want := proxykit.Proxy{Scheme: proxykit.HTTP, Host: "host:80"}
	for _, test := range []struct {
		format, input string
		strict        bool
	}{
		{"%t://%h|%h|%d", "http://old|host|80", true},
		{"%t://%h|%h|%d", "http://old|host|80", false},
		{"%t://%h|%h|%d", "http://|host|80", false},
		{"%t://%h|%d|%d", "http://host||80", false},
	} {
		parser := mustNew(t, test.format, test.strict)
		got, err := parser.ParseString(test.input)
		if err != nil || got != want {
			t.Errorf("ParseString(%q, %q, strict=%v) = %+v, %v; want %+v", test.format, test.input, test.strict, got, err, want)
		}
	}
}

// BenchmarkNewInvalidFormat measures rejection before and after long valid prefixes.
func BenchmarkNewInvalidFormat(b *testing.B) {
	for _, test := range []struct {
		name, format string
	}{
		{"first", "%x" + strings.Repeat("%h:", 4096)},
		{"last", strings.Repeat("%h:", 4096) + "%"},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = proxyparser.New(test.format, false)
			}
		})
	}
}

// BenchmarkParseIntoLongPorts measures long digit sequences and a typical port.
func BenchmarkParseIntoLongPorts(b *testing.B) {
	parser := mustNew(b, "%t://%h:%d", true)
	for _, test := range []struct {
		name, port string
	}{
		{"typical", "8080"},
		{"invalid", strings.Repeat("9", 65536)},
		{"zero", strings.Repeat("0", 65536)},
		{"leading-zero", strings.Repeat("0", 65536) + "80"},
	} {
		b.Run(test.name, func(b *testing.B) {
			input := "http://proxy.example.com:" + test.port
			var proxy proxykit.Proxy
			b.ReportAllocs()
			for b.Loop() {
				_ = parser.ParseInto(input, &proxy)
			}
		})
	}
}

// BenchmarkParseIntoInvalidIPv6 measures validation errors for bracketed literals.
func BenchmarkParseIntoInvalidIPv6(b *testing.B) {
	for _, test := range []struct {
		name, format, input string
	}{
		{"plain", "%t://%h:%d", "http://[::bad::]:8080"},
		{"credentials", "%t://%u:%p@%h:%d", "http://u:p@[::bad::]:8080"},
	} {
		b.Run(test.name, func(b *testing.B) {
			parser := mustNew(b, test.format, true)
			var proxy proxykit.Proxy
			if err := parser.ParseInto(test.input, &proxy); !errors.Is(err, proxykit.ErrInvalidHost) {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = parser.ParseInto(test.input, &proxy)
			}
		})
	}
}

// TestParseIPv6CustomFields checks that format delimiters do not split IPv6 hosts.
func TestParseIPv6CustomFields(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, host := range []string{"[::1]", "[2001:db8::1]", "[fe80::1%eth0]", "[::ffff:192.0.2.1]"} {
			for _, separator := range []string{":", "::", "|", "%"} {
				format := "(%t)%h" + strings.ReplaceAll(separator, "%", "%%") + "%d:%u:%p"
				input := "(http)" + host + separator + "8080:user:pass"
				want := proxykit.Proxy{Scheme: proxykit.HTTP, Host: host + ":8080", Username: "user", Password: "pass"}
				got, err := mustNew(t, format, strict).ParseString(input)
				if err != nil || got != want {
					t.Errorf("ParseString(%q, strict=%v) = %+v, %v; want %+v", input, strict, got, err, want)
				}
			}
		}
	}
}

// TestParseLenientOptionalPassword checks absent passwords and raw at signs in usernames.
func TestParseLenientOptionalPassword(t *testing.T) {
	parser := mustNew(t, "%t://%u:%p@%h:%d", false)
	for _, endpoint := range []string{"proxy.example.com:8080", "[::1]:8080", "[fe80::1%eth0]:8080"} {
		for _, credentials := range []struct{ input, username, password string }{
			{"user@", "user", ""},
			{"user:@", "user", ""},
			{"user:pass@", "user", "pass"},
			{"user@realm:pass@", "user@realm", "pass"},
			{"@", "", ""},
			{"", "", ""},
		} {
			input := "http://" + credentials.input + endpoint
			want := proxykit.Proxy{Scheme: proxykit.HTTP, Host: endpoint, Username: credentials.username, Password: credentials.password}
			got, err := parser.ParseString(input)
			if err != nil || got != want {
				t.Errorf("ParseString(%q) = %+v, %v; want %+v", input, got, err, want)
			}
		}
	}
}

// FuzzParseIPv6CustomFields checks semantic field boundaries for valid IPv6 endpoints.
func FuzzParseIPv6CustomFields(f *testing.F) {
	f.Add(uint64(0), uint64(1), uint16(8080), uint8(0))
	f.Add(uint64(0x20010db800000000), uint64(1), uint16(1080), uint8(1))
	f.Fuzz(func(t *testing.T, high, low uint64, port uint16, selector uint8) {
		if port == 0 {
			return
		}
		host := fmt.Sprintf("[%x:%x:%x:%x:%x:%x:%x:%x]", high>>48, high>>32&65535, high>>16&65535, high&65535, low>>48, low>>32&65535, low>>16&65535, low&65535)
		separator := []string{":", "::", "|", "%"}[selector%4]
		format := "(%t)%h" + strings.ReplaceAll(separator, "%", "%%") + "%d:%u:%p"
		input := fmt.Sprintf("(http)%s%s%d:user:pass", host, separator, port)
		want := proxykit.Proxy{Scheme: proxykit.HTTP, Host: fmt.Sprintf("%s:%d", host, port), Username: "user", Password: "pass"}
		parser := mustNew(t, format, selector&4 == 0)
		got, err := parser.ParseString(input)
		if err != nil || got != want {
			t.Fatalf("ParseString(%q) = %+v, %v; want %+v", input, got, err, want)
		}
	})
}

// BenchmarkParseIntoNumericSuffix measures numeric DNS suffixes against an IPv4 control.
func BenchmarkParseIntoNumericSuffix(b *testing.B) {
	for _, host := range []string{"proxy1", "proxy.example1", "192.0.2.146"} {
		b.Run(host, func(b *testing.B) {
			parser := mustNew(b, "%t://%h:%d", true)
			input := "http://" + host + ":8080"
			var proxy proxykit.Proxy
			if err := parser.ParseInto(input, &proxy); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = parser.ParseInto(input, &proxy)
			}
		})
	}
}
