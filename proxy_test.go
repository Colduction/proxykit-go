package proxykit_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
)

var _ proxykit.ProxyProvider = (*proxykit.Proxy)(nil)

func TestIsValidHostnamePort(t *testing.T) {
	tests := []struct {
		address string
		valid   bool
	}{
		{address: "proxy.example.com:8080", valid: true},
		{address: "127.0.0.1:1", valid: true},
		{address: "192.0.2.10:8080", valid: true},
		{address: "[::1]:80", valid: true},
		{address: "[2001:db8::1]:443", valid: true},
		{address: "[::ffff:192.0.2.10]:443", valid: true},
		{address: "[fe80::1%eth0]:80", valid: true},
		{address: "proxy-name:65535", valid: true},
		{address: "proxy.example.com.:8080", valid: true},
		{address: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ":80", valid: true},
		{address: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ".:80", valid: true},
		{address: ":80", valid: false},
		{address: "proxy.example.com:", valid: false},
		{address: "proxy.example.com", valid: false},
		{address: "proxy.example.com:0", valid: false},
		{address: "proxy.example.com:65536", valid: false},
		{address: "proxy..example.com:80", valid: false},
		{address: "proxy_example.com:80", valid: false},
		{address: strings.Repeat("a", 64) + ":80", valid: false},
		{address: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + ":80", valid: false},
		{address: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + ".:80", valid: false},
		{address: "-proxy.example.com:80", valid: false},
		{address: "proxy-.example.com:80", valid: false},
		{address: "proxy.-example.com:80", valid: false},
		{address: "proxy.example-.com:80", valid: false},
		{address: ".proxy.example.com:80", valid: false},
		{address: "proxy.example.com..:80", valid: false},
		{address: "[]:80", valid: false},
		{address: "[127.0.0.1]:80", valid: false},
		{address: "[proxy.example.com]:80", valid: false},
		{address: "[::1]80", valid: false},
		{address: "[::1]:", valid: false},
		{address: "::1:80", valid: false},
		{address: "123.example.com:80", valid: true},
		{address: "256.256.256.256:80", valid: false},
		{address: "01.02.03.04:80", valid: false},
		{address: "1.2.3:80", valid: false},
		{address: "1.2.3.4.5:80", valid: false},
		{address: "1234567890:80", valid: false},
		{address: "127.0.0.1.:80", valid: false},
		{address: "[2001:DB8::1]:80", valid: true},
		{address: "[fe80::1%eth0.100]:80", valid: true},
		{address: "[fe80::1%1]:80", valid: true},
		{address: "[fe80::1%25eth0]:80", valid: true},
		{address: "[fe80::1%a/b]:80", valid: false},
		{address: "[::1%eth0%foo]:80", valid: false},
		{address: "[fe80::1% ]:80", valid: false},
		{address: "[v1.fe80::1]:80", valid: false},
		{address: "proxy.example.com:0080", valid: true},
		{address: "proxy.example.com:000080", valid: true},
		{address: "proxy.example.com:00000", valid: false},
		{address: "proxy.example.com:+80", valid: false},
		{address: "proxy.example.com:1234567", valid: false},
		{address: "proxy.example.com:0000065535", valid: true},
		{address: "proxy.example.com:0000065536", valid: false},
		{address: "proxy.example.com:" + strings.Repeat("0", 1000) + "65535", valid: true},
		{address: "example.123:80", valid: false},
	}
	for _, test := range tests {
		if got := proxykit.IsValidHostnamePort(test.address); got != test.valid {
			t.Errorf("IsValidHostnamePort(%q) = %v, want %v", test.address, got, test.valid)
		}
	}
}

func TestIsValidHost(t *testing.T) {
	tests := []struct {
		host  string
		valid bool
	}{
		{host: "proxy.example.com", valid: true},
		{host: "proxy.example.com.", valid: true},
		{host: "proxy-name", valid: true},
		{host: "123.example.com", valid: true},
		{host: "a-1.b-2", valid: true},
		{host: "example.123", valid: false},
		{host: "xn--bcher-kva.example", valid: true},
		{host: "127.0.0.1", valid: true},
		{host: "::1", valid: true},
		{host: "2001:db8::1", valid: true},
		{host: "::ffff:192.0.2.10", valid: true},
		{host: "fe80::1%eth0", valid: true},
		{host: "", valid: false},
		{host: ".", valid: false},
		{host: "123", valid: false},
		{host: "256.256.256.256", valid: false},
		{host: "01.02.03.04", valid: false},
		{host: "1234567890", valid: false},
		{host: "127.0.0.1.", valid: false},
		{host: "::1.", valid: false},
		{host: "[::1]", valid: false},
		{host: "fe80::1%a/b", valid: false},
		{host: "fe80::1%25eth0", valid: true},
		{host: "host_name", valid: false},
		{host: "bücher.example", valid: false},
		{host: "-proxy.example.com", valid: false},
		{host: "proxy..example.com", valid: false},
	}
	for _, test := range tests {
		if got := proxykit.IsValidHost(test.host); got != test.valid {
			t.Errorf("IsValidHost(%q) = %v, want %v", test.host, got, test.valid)
		}
	}
}

func TestProxyValidatorMethods(t *testing.T) {
	proxy := &proxykit.Proxy{
		Scheme:   proxykit.HTTP,
		Host:     "proxy.example.com:8080",
		Username: "user",
		Password: "pass",
	}
	if !proxy.IsValidScheme() {
		t.Error("IsValidScheme() = false, want true")
	}
	if !proxy.IsValidHostnamePort() {
		t.Error("IsValidHostnamePort() = false, want true")
	}
	if !proxy.IsValidCredentials() {
		t.Error("IsValidCredentials() = false, want true")
	}
	if !proxy.IsCredentialFilled() {
		t.Error("IsCredentialFilled() = false, want true")
	}
	if !proxy.IsValid() {
		t.Error("IsValid() = false, want true")
	}
	if proxy.IsZero() {
		t.Error("IsZero() = true, want false")
	}

	var nilProxy *proxykit.Proxy
	if nilProxy.IsValidScheme() ||
		nilProxy.IsValidHostnamePort() ||
		nilProxy.IsValidCredentials() ||
		nilProxy.IsCredentialFilled() ||
		nilProxy.IsValid() {
		t.Error("nil proxy validation methods must report false")
	}
	if !nilProxy.IsZero() {
		t.Error("nil proxy IsZero() = false, want true")
	}
	var validator proxykit.ProxyValidator = nilProxy
	if !validator.IsZero() {
		t.Error("nil proxy IsZero() through ProxyValidator = false, want true")
	}
}

func TestProxyIsValid(t *testing.T) {
	tests := []struct {
		name  string
		proxy proxykit.Proxy
		valid bool
	}{
		{name: "endpoint", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS5, Host: "proxy.example.com:1080"}, valid: true},
		{name: "credentials", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "192.0.2.10:8080", Username: "user", Password: "pass"}, valid: true},
		{name: "username only", proxy: proxykit.Proxy{Scheme: proxykit.HTTPS, Host: "[2001:db8::1]:443", Username: "user"}, valid: true},
		{name: "zero", proxy: proxykit.Proxy{}, valid: false},
		{name: "empty host", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: ":80"}, valid: false},
		{name: "missing port", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com"}, valid: false},
		{name: "unsupported scheme", proxy: proxykit.Proxy{Scheme: "ftp", Host: "proxy.example.com:21"}, valid: false},
		{name: "uppercase scheme", proxy: proxykit.Proxy{Scheme: "HTTP", Host: "proxy.example.com:80"}, valid: false},
		{name: "password without username", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Password: "pass"}, valid: false},
		{name: "control byte in password", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Username: "user", Password: "pa\r\nss"}, valid: false},
		{name: "oversized username", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Username: strings.Repeat("u", 256)}, valid: false},
		{name: "non-ascii password", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Username: "user", Password: "pé"}, valid: false},
		{name: "http colon in username", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Username: "us:er", Password: "pass"}, valid: false},
		{name: "socks5 colon in username", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS5, Host: "proxy.example.com:1080", Username: "us:er", Password: "pass"}, valid: true},
		{name: "socks4 userid", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS4, Host: "192.0.2.10:1080", Username: "user"}, valid: true},
		{name: "socks4a password", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS4A, Host: "proxy.example.com:1080", Username: "user", Password: "pass"}, valid: false},
	}
	for _, test := range tests {
		if got := test.proxy.IsValid(); got != test.valid {
			t.Errorf("%s: IsValid() = %v, want %v", test.name, got, test.valid)
		}
	}
}

func TestExportURL(t *testing.T) {
	tests := []struct {
		name  string
		proxy proxykit.Proxy
		want  string
	}{
		{name: "endpoint", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS5H, Host: "proxy.example.com:1080"}, want: "socks5h://proxy.example.com:1080"},
		{name: "username only", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user"}, want: "http://user@proxy.example.com:8080"},
		{name: "credentials", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user", Password: "pass"}, want: "http://user:pass@proxy.example.com:8080"},
		{name: "escaped credentials", proxy: proxykit.Proxy{Scheme: proxykit.HTTPS, Host: "proxy.example.com:443", Username: "us:er", Password: "p@ss/w"}, want: "https://us%3Aer:p%40ss%2Fw@proxy.example.com:443"},
		{name: "ipv6 zone", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[fe80::1%eth0]:80"}, want: "http://[fe80::1%25eth0]:80"},
	}
	for _, test := range tests {
		u := test.proxy.ExportURL()
		if u == nil {
			t.Errorf("%s: ExportURL() = nil", test.name)
			continue
		}
		got := u.String()
		if got != test.want {
			t.Errorf("%s: ExportURL().String() = %q, want %q", test.name, got, test.want)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Errorf("%s: url.Parse(%q) failed: %v", test.name, got, err)
			continue
		}
		if parsed.Host != test.proxy.Host {
			t.Errorf("%s: url.Parse(%q).Host = %q, want %q", test.name, got, parsed.Host, test.proxy.Host)
		}
	}

	u := (&proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user"}).ExportURL()
	if _, set := u.User.Password(); set {
		t.Error("ExportURL() reports a set password for a username-only proxy")
	}

	var nilProxy *proxykit.Proxy
	if nilProxy.ExportURL() != nil {
		t.Error("nil proxy ExportURL() != nil")
	}
}

func TestProxySettersAndReset(t *testing.T) {
	var proxy proxykit.Proxy
	proxy.SetScheme(proxykit.SOCKS5)
	proxy.SetHost("proxy.example.com:1080")
	proxy.SetUsername("user")
	proxy.SetPassword("pass")
	want := proxykit.Proxy{
		Scheme:   proxykit.SOCKS5,
		Host:     "proxy.example.com:1080",
		Username: "user",
		Password: "pass",
	}
	if proxy != want {
		t.Errorf("setters produced %+v, want %+v", proxy, want)
	}
	if proxy.GetScheme() != want.Scheme ||
		proxy.GetHost() != want.Host ||
		proxy.GetUsername() != want.Username ||
		proxy.GetPassword() != want.Password {
		t.Error("getters must return the values set")
	}

	long := strings.Repeat("x", 256)
	proxy.SetUsername(long)
	proxy.SetPassword(long)
	if proxy.Username != long || proxy.Password != long {
		t.Error("SetUsername and SetPassword must store values without validating them")
	}
	if proxy.IsValidCredentials() {
		t.Error("IsValidCredentials() = true for 256-byte credentials, want false")
	}

	proxy.Reset()
	if !proxy.IsZero() {
		t.Errorf("Reset() left %+v, want zero", proxy)
	}

	var nilProxy *proxykit.Proxy
	nilProxy.SetScheme(proxykit.HTTP)
	nilProxy.SetHost("proxy.example.com:80")
	nilProxy.SetUsername("user")
	nilProxy.SetPassword("pass")
	nilProxy.Reset()
	if nilProxy.GetScheme() != "" ||
		nilProxy.GetHost() != "" ||
		nilProxy.GetUsername() != "" ||
		nilProxy.GetPassword() != "" {
		t.Error("nil proxy getters must return zero values")
	}
}

func TestParseScheme(t *testing.T) {
	tests := []struct {
		in   string
		want proxykit.ProxyScheme
		ok   bool
	}{
		{in: "http", want: proxykit.HTTP, ok: true},
		{in: "HTTP", want: proxykit.HTTP, ok: true},
		{in: "Https", want: proxykit.HTTPS, ok: true},
		{in: "socks4", want: proxykit.SOCKS4, ok: true},
		{in: "SOCKS4A", want: proxykit.SOCKS4A, ok: true},
		{in: "socks5", want: proxykit.SOCKS5, ok: true},
		{in: "SoCkS5H", want: proxykit.SOCKS5H, ok: true},
		{in: ""},
		{in: "socks"},
		{in: "ftp"},
		{in: "socks5hh"},
		{in: "http "},
		{in: "\u017focks5"},
	}
	for _, test := range tests {
		got, ok := proxykit.ParseScheme(test.in)
		if got != test.want || ok != test.ok {
			t.Errorf("ParseScheme(%q) = (%q, %v), want (%q, %v)", test.in, got, ok, test.want, test.ok)
		}
	}
	if allocs := testing.AllocsPerRun(1_000, func() { proxykit.ParseScheme("SOCKS5H") }); allocs != 0 {
		t.Errorf("ParseScheme allocated %v times per run, want 0", allocs)
	}
}

func TestSchemeMethods(t *testing.T) {
	ports := map[proxykit.ProxyScheme]uint16{
		proxykit.HTTP:    80,
		proxykit.HTTPS:   443,
		proxykit.SOCKS4:  1080,
		proxykit.SOCKS4A: 1080,
		proxykit.SOCKS5:  1080,
		proxykit.SOCKS5H: 1080,
	}
	for scheme, port := range ports {
		if !scheme.IsValid() || !proxykit.IsValidScheme(scheme) {
			t.Errorf("%q must be a valid scheme", scheme)
		}
		if got := scheme.DefaultPort(); got != port {
			t.Errorf("%q.DefaultPort() = %d, want %d", scheme, got, port)
		}
		if scheme.String() != string(scheme) {
			t.Errorf("%q.String() = %q", scheme, scheme.String())
		}
	}
	for _, scheme := range []proxykit.ProxyScheme{"", "HTTP", "socks", "ftp", "socks5s"} {
		if scheme.IsValid() || proxykit.IsValidScheme(scheme) {
			t.Errorf("%q must not be a valid scheme", scheme)
		}
		if got := scheme.DefaultPort(); got != 0 {
			t.Errorf("%q.DefaultPort() = %d, want 0", scheme, got)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		proxy proxykit.Proxy
		err   error
	}{
		{name: "valid", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS5H, Host: "proxy.example.com:1080", Username: "user", Password: "pass"}},
		{name: "zero", proxy: proxykit.Proxy{}, err: proxykit.ErrInvalidScheme},
		{name: "unsupported scheme", proxy: proxykit.Proxy{Scheme: "ftp", Host: "proxy.example.com:21"}, err: proxykit.ErrInvalidScheme},
		{name: "uppercase scheme", proxy: proxykit.Proxy{Scheme: "HTTP", Host: "proxy.example.com:80"}, err: proxykit.ErrInvalidScheme},
		{name: "missing port", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com"}, err: proxykit.ErrInvalidPort},
		{name: "bracket missing port", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[::1]"}, err: proxykit.ErrInvalidPort},
		{name: "lone bracket", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "["}, err: proxykit.ErrInvalidPort},
		{name: "empty host and port", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: ""}, err: proxykit.ErrInvalidHost},
		{name: "unterminated bracket", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[::1:80"}, err: proxykit.ErrInvalidHost},
		{name: "empty port", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:"}, err: proxykit.ErrInvalidPort},
		{name: "port zero", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:0"}, err: proxykit.ErrInvalidPort},
		{name: "port range", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:65536"}, err: proxykit.ErrInvalidPort},
		{name: "empty host", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: ":80"}, err: proxykit.ErrInvalidHost},
		{name: "bad host", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "bad host:80"}, err: proxykit.ErrInvalidHost},
		{name: "unbracketed ipv6", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "::1:80"}, err: proxykit.ErrInvalidHost},
		{name: "bad literal", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[proxy.example.com]:80"}, err: proxykit.ErrInvalidHost},
		{name: "password without username", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Password: "pass"}, err: proxykit.ErrInvalidCredentials},
		{name: "http colon in username", proxy: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80", Username: "us:er", Password: "pass"}, err: proxykit.ErrInvalidCredentials},
		{name: "socks4 password", proxy: proxykit.Proxy{Scheme: proxykit.SOCKS4, Host: "192.0.2.10:1080", Username: "user", Password: "pass"}, err: proxykit.ErrInvalidCredentials},
	}
	for _, test := range tests {
		err := test.proxy.Validate()
		if test.err == nil {
			if err != nil {
				t.Errorf("%s: Validate() = %v, want nil", test.name, err)
			}
			continue
		}
		if !errors.Is(err, test.err) {
			t.Errorf("%s: Validate() = %v, want %v", test.name, err, test.err)
		}
		if test.proxy.IsValid() {
			t.Errorf("%s: IsValid() = true, want false", test.name)
		}
	}

	var nilProxy *proxykit.Proxy
	if err := nilProxy.Validate(); !errors.Is(err, proxykit.ErrNilProxy) {
		t.Errorf("nil proxy Validate() = %v, want ErrNilProxy", err)
	}

	valid := &proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user", Password: "pass"}
	invalid := &proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com"}
	if allocs := testing.AllocsPerRun(1_000, func() {
		_ = valid.Validate()
		_ = invalid.Validate()
	}); allocs != 0 {
		t.Errorf("Validate allocated %v times per run, want 0", allocs)
	}
	badLiteral := &proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[proxy.example.com]:80"}
	if allocs := testing.AllocsPerRun(1_000, func() { _ = badLiteral.Validate() }); allocs != 1 {
		t.Errorf("Validate on a malformed bracketed literal allocated %v times per run, want the documented 1", allocs)
	}
}

func TestFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want proxykit.Proxy
		err  error
	}{
		{name: "full", url: "socks5h://user:pass@proxy.example.com:1080", want: proxykit.Proxy{Scheme: proxykit.SOCKS5H, Host: "proxy.example.com:1080", Username: "user", Password: "pass"}},
		{name: "default http port", url: "http://proxy.example.com", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80"}},
		{name: "default https port", url: "https://proxy.example.com", want: proxykit.Proxy{Scheme: proxykit.HTTPS, Host: "proxy.example.com:443"}},
		{name: "default socks port ipv6", url: "socks5://[::1]", want: proxykit.Proxy{Scheme: proxykit.SOCKS5, Host: "[::1]:1080"}},
		{name: "uppercase scheme", url: "HTTP://proxy.example.com:8080", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080"}},
		{name: "no scheme", url: "//proxy.example.com:8080", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080"}},
		{name: "trailing slash", url: "http://proxy.example.com:8080/", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080"}},
		{name: "encoded credentials", url: "http://us%40er:p%40ss%2Fw@proxy.example.com:8080", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "us@er", Password: "p@ss/w"}},
		{name: "username only", url: "http://user@proxy.example.com:8080", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user"}},
		{name: "ipv6 zone", url: "http://[fe80::1%25eth0]:80", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "[fe80::1%eth0]:80"}},
		{name: "empty port", url: "http://proxy.example.com:", want: proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:80"}},
		{name: "empty port ipv6", url: "socks5h://[::1]:", want: proxykit.Proxy{Scheme: proxykit.SOCKS5H, Host: "[::1]:1080"}},
		{name: "path", url: "http://proxy.example.com:8080/path", err: proxykit.ErrInvalidProxyURL},
		{name: "query", url: "http://proxy.example.com:8080?x=1", err: proxykit.ErrInvalidProxyURL},
		{name: "force query", url: "http://proxy.example.com:8080?", err: proxykit.ErrInvalidProxyURL},
		{name: "fragment", url: "http://proxy.example.com:8080#f", err: proxykit.ErrInvalidProxyURL},
		{name: "bare host is opaque", url: "proxy.example.com:8080", err: proxykit.ErrInvalidProxyURL},
		{name: "unsupported scheme", url: "ftp://proxy.example.com:21", err: proxykit.ErrInvalidScheme},
		{name: "empty host", url: "http://:80", err: proxykit.ErrInvalidHost},
		{name: "no host", url: "http:///", err: proxykit.ErrInvalidHost},
		{name: "bad port", url: "http://proxy.example.com:99999", err: proxykit.ErrInvalidPort},
		{name: "password only", url: "http://:pass@proxy.example.com:80", err: proxykit.ErrInvalidCredentials},
		{name: "colon in username", url: "http://us%3Aer:pass@proxy.example.com:80", err: proxykit.ErrInvalidCredentials},
	}
	for _, test := range tests {
		u, err := url.Parse(test.url)
		if err != nil {
			t.Fatalf("%s: url.Parse(%q): %v", test.name, test.url, err)
		}
		got, err := proxykit.FromURL(u)
		if test.err != nil {
			if !errors.Is(err, test.err) {
				t.Errorf("%s: FromURL() error = %v, want %v", test.name, err, test.err)
			}
			if !got.IsZero() {
				t.Errorf("%s: FromURL() = %+v on error, want zero", test.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: FromURL() error = %v", test.name, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s: FromURL() = %+v, want %+v", test.name, got, test.want)
		}
		back, err := proxykit.FromURL(got.ExportURL())
		if err != nil || back != got {
			t.Errorf("%s: FromURL(ExportURL()) = (%+v, %v), want %+v", test.name, back, err, got)
		}
	}

	if _, err := proxykit.FromURL(nil); !errors.Is(err, proxykit.ErrNilURL) {
		t.Errorf("FromURL(nil) error = %v, want ErrNilURL", err)
	}
}

func TestIsValidCredentialsFor(t *testing.T) {
	tests := []struct {
		scheme             proxykit.ProxyScheme
		username, password string
		valid              bool
	}{
		{scheme: proxykit.HTTP, valid: true},
		{scheme: proxykit.HTTP, username: "user", valid: true},
		{scheme: proxykit.HTTP, username: "user", password: "pa:ss", valid: true},
		{scheme: proxykit.HTTP, username: "us:er", password: "pass"},
		{scheme: proxykit.HTTPS, username: "us:er"},
		{scheme: proxykit.SOCKS5, username: "us:er", password: "pa:ss", valid: true},
		{scheme: proxykit.SOCKS5H, username: "user", valid: true},
		{scheme: proxykit.SOCKS5, username: strings.Repeat("u", 255), password: strings.Repeat("p", 255), valid: true},
		{scheme: proxykit.SOCKS5, username: strings.Repeat("u", 256)},
		{scheme: proxykit.SOCKS4, username: "user", valid: true},
		{scheme: proxykit.SOCKS4, username: "user", password: "pass"},
		{scheme: proxykit.SOCKS4A, password: "pass"},
		{scheme: "ftp", username: "us:er", password: "pass", valid: true},
		{scheme: proxykit.HTTP, username: "user", password: "p\x7f"},
		{scheme: proxykit.HTTP, username: "user", password: "p\xff"},
		{scheme: proxykit.HTTP, username: "ü"},
	}
	for _, test := range tests {
		if got := proxykit.IsValidCredentialsFor(test.scheme, test.username, test.password); got != test.valid {
			t.Errorf("IsValidCredentialsFor(%q, %q, %q) = %v, want %v", test.scheme, test.username, test.password, got, test.valid)
		}
	}
	if proxykit.IsValidCredentials("us:er", "pass") != true || proxykit.IsValidCredentials("", "pass") {
		t.Error("IsValidCredentials must apply only the scheme-independent rules")
	}
}

func TestLogValue(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	proxy := &proxykit.Proxy{Scheme: proxykit.HTTP, Host: "proxy.example.com:8080", Username: "user", Password: "secret"}
	logger.Info("dial", "proxy", proxy)
	if out := buf.String(); strings.Contains(out, "secret") || !strings.Contains(out, "http://user:xxxxx@proxy.example.com:8080") {
		t.Errorf("slog output %q must redact the password", out)
	}

	buf.Reset()
	logger.Info("dial", "proxy", *proxy)
	if out := buf.String(); strings.Contains(out, "secret") || !strings.Contains(out, "http://user:xxxxx@proxy.example.com:8080") {
		t.Errorf("slog output %q must redact the password of a Proxy value", out)
	}

	buf.Reset()
	logger.Info("dial", "proxy", &proxykit.Proxy{Scheme: proxykit.SOCKS5, Host: "proxy.example.com:1080"})
	if out := buf.String(); !strings.Contains(out, "socks5://proxy.example.com:1080") {
		t.Errorf("slog output %q must contain the proxy URL", out)
	}

	buf.Reset()
	var nilProxy *proxykit.Proxy
	logger.Info("dial", "proxy", nilProxy)
	if out := buf.String(); !strings.Contains(out, "LogValue panicked") {
		t.Errorf("slog output %q must report a nil *Proxy as a LogValue panic", out)
	}
}

func TestValidatorsDoNotAllocate(t *testing.T) {
	inputs := []string{
		"proxy.example.com:8080",
		"proxy.example.com.:8080",
		"127.0.0.1:1",
		"[2001:db8::1]:443",
		"[fe80::1%eth0]:80",
	}
	for _, input := range inputs {
		if !proxykit.IsValidHostnamePort(input) {
			t.Fatalf("IsValidHostnamePort(%q) = false", input)
		}
		if allocs := testing.AllocsPerRun(100, func() { proxykit.IsValidHostnamePort(input) }); allocs != 0 {
			t.Errorf("IsValidHostnamePort(%q) allocated %v times per run, want 0", input, allocs)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		proxykit.IsValidCredentialsFor(proxykit.HTTP, "user", "pass")
		proxykit.IsValidCredentialsFor(proxykit.SOCKS5, "us:er", "pass")
	}); allocs != 0 {
		t.Errorf("IsValidCredentialsFor allocated %v times per run, want 0", allocs)
	}
}

func FuzzIsValidHostnamePort(f *testing.F) {
	for _, seed := range []string{
		"proxy.example.com:8080",
		"127.0.0.1:1",
		"[2001:db8::1]:443",
		"[fe80::1%eth0]:80",
		"256.256.256.256:80",
		":80",
		"[::1]",
		"proxy.example.com:0080",
		"xn--bcher-kva.example:443",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		valid := proxykit.IsValidHostnamePort(s)
		proxy := proxykit.Proxy{Scheme: proxykit.HTTP, Host: s}
		if err := proxy.Validate(); (err == nil) != valid {
			t.Fatalf("IsValidHostnamePort(%q) = %v but Validate() = %v", s, valid, err)
		}
		if !valid {
			return
		}
		host, port, _, ok := proxykit.SplitHostnamePort(s)
		if !ok || host == "" || port == "" {
			t.Fatalf("SplitHostnamePort(%q) = (%q, %q, _, %v) for a valid input", s, host, port, ok)
		}
	})
}

func FuzzIsValidCredentials(f *testing.F) {
	f.Add("user", "pass")
	f.Add("us:er", "pa:ss")
	f.Add("", "pass")
	f.Add("user", "")
	f.Add(strings.Repeat("u", 255), strings.Repeat("p", 255))
	f.Fuzz(func(t *testing.T, username, password string) {
		valid := proxykit.IsValidCredentials(username, password)
		if got := proxykit.IsValidCredentialsFor(proxykit.SOCKS5, username, password); got != valid {
			t.Fatalf("IsValidCredentialsFor(socks5, %q, %q) = %v, IsValidCredentials = %v", username, password, got, valid)
		}
		if proxykit.IsValidCredentialsFor(proxykit.HTTP, username, password) && strings.IndexByte(username, ':') >= 0 {
			t.Fatalf("IsValidCredentialsFor(http, %q, %q) accepted a colon in the username", username, password)
		}
		if proxykit.IsValidCredentialsFor(proxykit.SOCKS4, username, password) && password != "" {
			t.Fatalf("IsValidCredentialsFor(socks4, %q, %q) accepted a password", username, password)
		}
		if valid && (len(username) > 255 || len(password) > 255 || (username == "" && password != "")) {
			t.Fatalf("IsValidCredentials(%q, %q) = true, want false", username, password)
		}
	})
}

func BenchmarkIsValidHostnamePort(b *testing.B) {
	cases := []string{
		"proxy.example.com:8080",
		"127.0.0.1:1",
		"192.0.2.10:8080",
		"[::1]:80",
		"[2001:db8::1]:443",
		"[::ffff:192.0.2.10]:443",
		"[fe80::1%eth0]:80",
		"proxy-name:65535",
		"proxy.example.com.:8080",
		":80",
		"proxy.example.com:0",
		"proxy.example.com:65536",
		"proxy..example.com:80",
		"proxy_under.example.com:80",
		strings.Repeat("a", 64) + ":80",
		"-proxy.example.com:80",
		"proxy-.example.com:80",
		"[]:80",
		"[127.0.0.1]:80",
		"[proxy.example.com]:80",
		"::1:80",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, address := range cases {
			_ = proxykit.IsValidHostnamePort(address)
		}
	}
}

func BenchmarkIsValidHost(b *testing.B) {
	cases := []string{
		"proxy.example.com",
		"proxy.example.com.",
		"127.0.0.1",
		"2001:db8::1",
		"fe80::1%eth0",
		"proxy-name",
		"-proxy.example.com",
		"proxy-.example.com",
		"proxy..example.com",
		"proxy_under.example.com",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, host := range cases {
			_ = proxykit.IsValidHost(host)
		}
	}
}

func BenchmarkSplitHostnamePort(b *testing.B) {
	cases := []string{
		"proxy.example.com:8080",
		"127.0.0.1:8080",
		"[2001:db8::1]:8080",
		"[fe80::1%eth0]:8080",
		":8080",
		"::1:8080",
		"[::1]8080",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, address := range cases {
			_, _, _, _ = proxykit.SplitHostnamePort(address)
		}
	}
}

func BenchmarkProxyValidatorMethods(b *testing.B) {
	proxy := &proxykit.Proxy{
		Scheme:   proxykit.HTTP,
		Host:     "proxy.example.com:8080",
		Username: "user",
		Password: "pass",
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = proxy.IsValidScheme()
		_ = proxy.IsValidHostnamePort()
		_ = proxy.IsValidCredentials()
		_ = proxy.IsValid()
	}
}

func BenchmarkExportURL(b *testing.B) {
	proxy := &proxykit.Proxy{
		Scheme:   proxykit.HTTP,
		Host:     "proxy.example.com:8080",
		Username: "user",
		Password: "pass",
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = proxy.ExportURL()
	}
}
