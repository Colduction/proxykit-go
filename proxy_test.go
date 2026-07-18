package proxykit_test

import (
	"strings"
	"testing"

	"github.com/colduction/proxykit-go"
)

var _ proxykit.ProxyValidator = (*proxykit.Proxy)(nil)

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
		{address: ":80", valid: true},
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
	}
	for _, test := range tests {
		if got := proxykit.IsValidHostnamePort(test.address); got != test.valid {
			t.Errorf("IsValidHostnamePort(%q) = %v, want %v", test.address, got, test.valid)
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

	var nilProxy *proxykit.Proxy
	if nilProxy.IsValidScheme() ||
		nilProxy.IsValidHostnamePort() ||
		nilProxy.IsValidCredentials() {
		t.Error("nil proxy validation methods must report false")
	}
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
