package proxyparser_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
)

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
