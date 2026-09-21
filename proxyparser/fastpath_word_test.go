package proxyparser_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
	"github.com/colduction/proxykit-go/proxyparser"
)

func isIPv4ByteWise(host string) bool {
	var octets, digits, value int
	for i := range len(host) {
		c := host[i]
		if c == '.' {
			if digits == 0 {
				return false
			}
			octets++
			digits, value = 0, 0
			continue
		}
		if c < '0' || c > '9' || (digits == 1 && value == 0) {
			return false
		}
		digits++
		value = value*10 + int(c-'0')
		if digits > 3 || value > 255 {
			return false
		}
	}
	return octets == 3 && digits > 0
}

func checkIPv4(t testing.TB, host string) {
	t.Helper()
	got := proxyparser.IsIPv4Fast(host)
	if want := isIPv4ByteWise(host); got != want {
		t.Fatalf("IsIPv4Fast(%q) = %v, want %v", host, got, want)
	}
	if got && !proxykit.IsValidHost(host) {
		t.Fatalf("IsIPv4Fast accepts %q, which IsValidHost rejects", host)
	}
}

func TestFastIPv4EveryOctetCombination(t *testing.T) {
	octets := []string{
		"", "0", "1", "5", "9", "00", "01", "09", "10", "25", "55", "99", "000", "001", "010", "099", "100", "199",
		"200", "249", "250", "255", "256", "259", "260", "299", "300", "999", "0255", "1000", "2550",
	}
	for _, a := range octets {
		for _, b := range octets {
			for _, c := range octets {
				for _, d := range octets {
					checkIPv4(t, a+"."+b+"."+c+"."+d)
				}
			}
		}
	}
	for _, a := range octets {
		for _, b := range octets {
			checkIPv4(t, a+"."+b)
			checkIPv4(t, a+"."+b+"."+a)
			checkIPv4(t, a+"."+b+"."+a+"."+b+"."+a)
			checkIPv4(t, a+b+"."+a+"."+b)
		}
	}
}

func TestFastIPv4EveryByteAtEveryPosition(t *testing.T) {
	addresses := []string{
		"1.2.3.4", "10.2.3.4", "1.20.3.4", "1.2.30.4", "1.2.3.40", "10.20.3.4", "1.2.30.40", "100.2.3.4", "1.2.3.255",
		"10.20.30.40", "192.0.2.146", "203.0.113.27", "1.255.255.255", "255.255.255.1", "255.255.255.25", "255.255.255.255",
		"249.199.100.99", "1.1.1.255", "255.1.1.1", "25.25.25.25",
	}
	for _, address := range addresses {
		text := []byte(address)
		for position := range text {
			for value := range 256 {
				text[position] = byte(value)
				checkIPv4(t, string(text))
			}
			text[position] = address[position]
		}
		for position := 0; position+1 < len(text); position++ {
			for _, pair := range []string{"..", "./", "/.", ":0", "0:", "\x80.", ".\x80", "\xff\xff", "\x00."} {
				copy(text[position:], pair)
				checkIPv4(t, string(text))
				copy(text[position:], address[position:position+2])
			}
		}
	}
}

func TestFastIPv4ExhaustiveShortHosts(t *testing.T) {
	const alphabet = "0129./"
	limit := 9
	if testing.Short() {
		limit = 7
	}
	var visit func(prefix []byte, remaining int)
	visit = func(prefix []byte, remaining int) {
		checkIPv4(t, string(prefix))
		if remaining == 0 {
			return
		}
		for _, b := range []byte(alphabet) {
			visit(append(prefix, b), remaining-1)
		}
	}
	visit(nil, limit)
	for n := range 20 {
		checkIPv4(t, strings.Repeat("1", n))
		checkIPv4(t, strings.Repeat(".", n))
		checkIPv4(t, strings.Repeat("1.", n))
	}
}

func FuzzFastIPv4(f *testing.F) {
	for _, seed := range []string{"1.2.3.4", "192.0.2.146", "255.255.255.255", "256.1.1.1", "01.2.3.4", "1..2.3", "1.2.3.4.5"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		checkIPv4(t, host)
	})
}

// TestFastPortEveryValue checks the port test over every digit string of up
// to six digits. It must accept nothing that [proxykit.IsValidHostnamePort]
// rejects, and up to five digits it must accept everything valid.
func TestFastPortEveryValue(t *testing.T) {
	for _, host := range []string{"a", "proxy.example.com", "192.0.2.146"} {
		for _, width := range []int{1, 2, 3, 4, 5, 6} {
			limit := 1
			for range width {
				limit *= 10
			}
			for value := range limit {
				hostPort := fmt.Sprintf("%s:%0*d", host, width, value)
				got, want := proxyparser.IsHostPortFast(hostPort, false), proxykit.IsValidHostnamePort(hostPort)
				if got && !want || width <= 5 && got != want {
					t.Fatalf("IsHostPortFast(%q) = %v, IsValidHostnamePort = %v", hostPort, got, want)
				}
			}
		}
	}
	for _, port := range []string{"", "8a80", "80a", "a80", "8:80", "80:", "+80", "-80", " 80", "80 ", "\xb880", "8\x0080", "８０"} {
		for _, host := range []string{"a", "proxy.example.com"} {
			hostPort := host + ":" + port
			if proxyparser.IsHostPortFast(hostPort, false) && !proxykit.IsValidHostnamePort(hostPort) {
				t.Fatalf("IsHostPortFast accepts %q, which IsValidHostnamePort rejects", hostPort)
			}
		}
	}
}

// TestFastCredentialsMatchScalar puts every short string over an alphabet of
// delimiters, their neighbors in code, and unprintable bytes where the
// credentials belong, at every alignment to the words of the search.
func TestFastCredentialsMatchScalar(t *testing.T) {
	const alphabet = "u:@;A\x1f\x7f\x80"
	limit := 6
	if testing.Short() {
		limit = 4
	}
	initial := structuralindex.SetBackend(structuralindex.Portable)
	defer structuralindex.SetBackend(initial)
	parser := mustNew(t, "%t://%u:%p@%h:%d", true)
	prefixes := []string{"http://", "socks5://", "socks4://", "https://" + strings.Repeat("u", 7), "socks5h://" + strings.Repeat("u", 61)}
	suffixes := []string{"h:1", "proxy.example.com:8080", "@192.0.2.146:80", ":p@h.example:80", "\x7f.example:80", strings.Repeat("h", 70) + ":80"}
	var visit func(middle []byte, remaining int)
	visit = func(middle []byte, remaining int) {
		for _, prefix := range prefixes {
			for _, suffix := range suffixes {
				checkMatchesScalar(t, structuralindex.Portable, parser, "%t://%u:%p@%h:%d", prefix+string(middle)+suffix)
			}
		}
		if remaining == 0 {
			return
		}
		for _, b := range []byte(alphabet) {
			visit(append(middle, b), remaining-1)
		}
	}
	visit(nil, limit)
}

func TestFastSchemeMatchesScalar(t *testing.T) {
	schemes := []string{
		"http", "https", "socks4", "socks4a", "socks5", "socks5h", "HTTP", "Http", "httpx", "htt", "httpss", "socks", "socks6",
		"socks4h", "socks5a", "socks4A", "socks5H", "sockss", "socks55", "socks5hh", "ttp", "", "s", "ftp",
	}
	separators := []string{"://", ":/", "//", ":///", "::/", ":/:", "//:", "", ":", "/"}
	rests := []string{"proxy.example.com:8080", "u:p@proxy.example.com:8080", "a:1", "u:p@a:1", "/a:1", ":a:1"}
	eachBackend(t, func(backend structuralindex.Backend) {
		for _, format := range []string{"%t://%h:%d", "%t://%u:%p@%h:%d"} {
			parser := mustNew(t, format, true)
			for _, scheme := range schemes {
				for _, separator := range separators {
					for _, rest := range rests {
						checkMatchesScalar(t, backend, parser, format, scheme+separator+rest)
					}
				}
			}
		}
	})
}
