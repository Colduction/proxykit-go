package proxyparser

import (
	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
)

// ParseIntoScalar parses like [Parse.ParseInto] with the fast paths disabled.
func (pp *Parse) ParseIntoScalar(input string, proxy *proxykit.Proxy) error {
	if proxy == nil {
		return ErrNilProxy
	}
	return pp.parseInto(input, proxy, false)
}

// IsHostPortFast exposes the fast host and port test. With indexed set it
// uses the structural index and reports false when none can be built.
func IsHostPortFast(hostPort string, indexed bool) bool {
	var ix structuralindex.Index
	if indexed && !ix.Build(hostPort) {
		return false
	}
	return isHostPort(&ix, indexed, hostPort, 0)
}

// IsIPv4Fast exposes the fast dotted IPv4 test.
func IsIPv4Fast(host string) bool {
	return isIPv4(host)
}
