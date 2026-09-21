package proxyparser_test

import (
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
	"github.com/colduction/proxykit-go/proxyparser"
)

func eachBackend(t testing.TB, run func(backend structuralindex.Backend)) {
	t.Helper()
	initial := structuralindex.ActiveBackend()
	defer structuralindex.SetBackend(initial)
	for _, backend := range availableBackends() {
		structuralindex.SetBackend(backend)
		run(backend)
	}
}

func checkMatchesScalar(t testing.TB, backend structuralindex.Backend, parser *proxyparser.Parse, format, input string) {
	t.Helper()
	if diff := diffEntryPoints(parser, input); diff != "" {
		t.Fatalf("%v: format %q: %s", backend, format, diff)
	}
}

func TestFastHostPortEdges(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	hostPorts := []string{
		"a:1", "a:0", "a:00", "a:00080", "a:000080", "a:65535", "a:65536", "a:99999", "a:100000", "a:", ":80", "a:8a",
		"a:b:80", "a.-b:80", "a-.b:80", "-a:80", "a-:80", ".a:80", "a.:80", "a..b:80", ".:80", "a_b:80", "a b:80",
		"1:80", "123:80", "1.2.3:80", "1.2.3.4:80", "1.2.3.4.5:80", "1.2.3.4.:80", "a.1:80", "x-1.5:80", "0x7f.1:80",
		"0.0.0.0:1", "255.255.255.255:65535", "256.1.1.1:80", "01.2.3.4:80", "1.2.3.04:80", "1.2.3.4000:80", "1..3.4:80",
		"proxy1:80", "proxy.example1:80", "[::1]:80", "[1.2.3.4]:80", "::1:80", "a@b:80", "a`b:80", "a{b:80", "a\x7fb:80",
		"a\x80b:80", "\xe1.com:80", "a\x00b:80", "A-Z.example.COM:443", "xn--bcher-kva.example:8080",
		label63 + ":1", label63[:62] + ":1", label63[:60] + ".a:1", label63 + "a:1",
		strings.Repeat("a.", 30) + "a:1", strings.Repeat("a.", 31) + "a:1", strings.Repeat("ab-cd.", 9) + "example.com:8080",
	}
	for position := range 20 {
		for _, pair := range []string{"..", ".-", "-.", "--", "a."} {
			name := []byte(strings.Repeat("a", 22))
			copy(name[position:], pair)
			hostPorts = append(hostPorts, string(name)+":8080")
		}
	}
	eachBackend(t, func(backend structuralindex.Backend) {
		for _, hostPort := range hostPorts {
			for _, indexed := range []bool{false, backend != structuralindex.Portable} {
				if proxyparser.IsHostPortFast(hostPort, indexed) && !proxykit.IsValidHostnamePort(hostPort) {
					t.Errorf("%v indexed=%v: fast test accepts %q, which IsValidHostnamePort rejects", backend, indexed, hostPort)
				}
			}
		}
	})
}

var scalarParityFormats = []struct {
	format string
	strict bool
}{
	{"%t://%h:%d", true},
	{"%t://%u:%p@%h:%d", true},
	{"%t://%u:%p@%h:%d", false},
	{"%t://%u@%h:%d", false},
	{"%t://%h", false},
	{"%h:%d", false},
	{"(%t)%h:%d:%u:%p", false},
	{"(%t)%h:%d:%u:%p", true},
	{"%h:%d:%u:%p", false},
	{"%u:%p@%h:%d", false},
	{"%t://%h:%d!", true},
	{"%t-%h.%d/%u@%p", true},
	{"%h %d", false},
	{"%h%d", false},
	{"%d:%h", false},
	{"", false},
}

func TestParseMatchesScalar(t *testing.T) {
	long := strings.Repeat("u", 40)
	inputs := []string{
		"", "h", "http://", "http://a:1", "http://a:0", "https://a.b:65535", "https://a.b:65536", "socks4://u@1.2.3.4:1080",
		"socks4://u:p@1.2.3.4:1080", "socks4a://u:@proxy.example.com:1080", "socks5://:p@h.example:1", "socks5h://u:p@h.example:1",
		"HTTP://proxy.example.com:80", "Socks5://u:p@proxy.example.com:1080", "ftp://proxy.example.com:21", "http:/proxy.example.com:80",
		"http://proxy.example.com", "http://proxy.example.com:", "http://:80", "http://u:p@:80", "http://u:p@h", "http://u:p-h:80",
		"http://u:p@q@proxy.example.com:80", "http://u:p:q@proxy.example.com:80", "http://u\x7f:p@proxy.example.com:80",
		"http://u:p\x00@proxy.example.com:80", "http://\xffu:p@proxy.example.com:80", "http://u:p@[2001:db8::1]:8080",
		"http://[2001:db8::1]:8080", "http://[::1]8080", "http://proxy.example.com.:8080", "http://192.0.2.146:8080",
		"http://192.0.2.256:8080", "http://192.0.2:8080", "http://proxy-.example.com:8080", "http://proxy1:8080",
		"(http)res-us.lightningproxies.net:9999:admin:pass", "(HTTP)proxy.example.com:80", "(socks4)proxy.example.com:80:user:pass",
		"http)proxy.example.com:8080", "proxy.example.com:8080", "proxy.example.com:8080:user:pass", "user:pass@proxy.example.com:8080",
		"http://proxy.example.com:8080!", "http://proxy.example.com:8080!extra", "http-proxy.8080/user@pass",
		"http://" + long + ":" + long + "@proxy.example.com:8080", "http://" + long + ":p@" + long + ".example.com:8080",
		"http://" + strings.Repeat("a", 45) + ".example:8080", "http://" + strings.Repeat("a", 46) + ".example:8080",
		"http://" + strings.Repeat("a", 47) + ".example:8080", "socks5://u:p@" + strings.Repeat("a", 41) + ".example:8080",
	}
	eachBackend(t, func(backend structuralindex.Backend) {
		for _, f := range scalarParityFormats {
			parser := mustNew(t, f.format, f.strict)
			for _, input := range inputs {
				checkMatchesScalar(t, backend, parser, f.format, input)
			}
		}
	})
}

func FuzzParseMatchesScalar(f *testing.F) {
	for i, format := range scalarParityFormats {
		f.Add(format.format, "http://proxy.example.com:8080", format.strict)
		f.Add(format.format, "socks5://alice:s3cr3t@203.0.113.27:1080", format.strict)
		f.Add(format.format, "(http)res-us.lightningproxies.net:9999:admin:pass", i%2 == 0)
	}
	f.Fuzz(func(t *testing.T, format, input string, strict bool) {
		parser, err := proxyparser.New(format, strict)
		if err != nil {
			return
		}
		eachBackend(t, func(backend structuralindex.Backend) {
			checkMatchesScalar(t, backend, parser, format, input)
		})
	})
}
