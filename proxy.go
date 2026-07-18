// Package proxykit defines proxy addresses, schemes, credentials, and
// validation helpers.
package proxykit

import (
	"net/netip"
	"net/url"
)

// ProxyScheme identifies a proxy protocol accepted by [IsValidScheme].
type ProxyScheme string

// String returns the string representation of s.
func (s ProxyScheme) String() string {
	return string(s)
}

const (
	// HTTP is the [ProxyScheme] for an HTTP proxy.
	HTTP ProxyScheme = "http"
	// HTTPS is the [ProxyScheme] for an HTTPS proxy.
	HTTPS ProxyScheme = "https"
	// SOCKS5 is the [ProxyScheme] for a SOCKS5 proxy.
	SOCKS5 ProxyScheme = "socks5"
	// SOCKS5H is the [ProxyScheme] for a SOCKS5 proxy that resolves hostnames remotely.
	SOCKS5H ProxyScheme = "socks5h"
)

// DNS presentation lengths derive from the 255-octet wire name limit,
// including label length octets and the root label.
const (
	maxDNSLabelLength        = 63
	maxDNSNameLength         = 253
	maxAbsoluteDNSNameLength = 254
)

// ProxyProvider combines [ProxyGetter] and [ProxySetter].
type ProxyProvider interface {
	ProxyGetter
	ProxySetter
}

// ProxyGetter describes read access to proxy fields and validation.
type ProxyGetter interface {
	// ExportURL returns the proxy as a [url.URL].
	ExportURL() *url.URL

	// GetHost returns the proxy host and port.
	GetHost() string

	// GetPassword returns the proxy password.
	GetPassword() string

	// GetScheme returns the proxy scheme.
	GetScheme() ProxyScheme

	// GetUsername returns the proxy username.
	GetUsername() string
	ProxyValidator
}

// ProxySetter describes write access to proxy fields.
type ProxySetter interface {
	// SetHost sets the proxy host and port.
	SetHost(s string)

	// SetPassword sets the proxy password.
	SetPassword(s string)

	// SetScheme sets the proxy scheme.
	SetScheme(s string)

	// SetUsername sets the proxy username.
	SetUsername(s string)
	ProxyResetter
}

// ProxyValidator describes proxy state and validation methods.
type ProxyValidator interface {
	// IsCredentialFilled reports whether the proxy has a username.
	IsCredentialFilled() bool

	// IsValidCredentials reports whether the proxy credentials are valid.
	IsValidCredentials() bool

	// IsValidHostnamePort reports whether the proxy host and port are valid.
	IsValidHostnamePort() bool

	// IsValidScheme reports whether the proxy scheme is supported.
	IsValidScheme() bool

	// IsValid reports whether the proxy fields are valid.
	IsValid() bool

	// IsZero reports whether every proxy field is empty.
	IsZero() bool
}

// ProxyResetter describes a proxy reset operation.
type ProxyResetter interface {
	// Reset clears every proxy field.
	Reset()
}

// Proxy represents a proxy endpoint and its optional credentials.
type Proxy struct {
	Scheme   ProxyScheme `json:"scheme,omitempty" yaml:"scheme,omitempty" xml:"scheme,omitempty" cbor:"scheme,omitempty" bson:"scheme,omitempty" msgpack:"scheme,omitempty" toml:"scheme,omitempty" mapstructure:"scheme,omitempty" validate:"required_with=Host,omitempty,lowercase,oneof=http https socks5 socks5h"`
	Host     string      `json:"host,omitempty" yaml:"host,omitempty" xml:"host,omitempty" cbor:"host,omitempty" bson:"host,omitempty" msgpack:"host,omitempty" toml:"host,omitempty" mapstructure:"host,omitempty" validate:"required_with=Scheme,omitempty,hostname_port|tcp_addr"`
	Username string      `json:"username,omitempty" yaml:"username,omitempty" xml:"username,omitempty" cbor:"username,omitempty" bson:"username,omitempty" msgpack:"username,omitempty" toml:"username,omitempty" mapstructure:"username,omitempty" validate:"required_with_all=Scheme Host Password,omitempty,printascii,max=255"`
	Password string      `json:"password,omitempty" yaml:"password,omitempty" xml:"password,omitempty" cbor:"password,omitempty" bson:"password,omitempty" msgpack:"password,omitempty" toml:"password,omitempty" mapstructure:"password,omitempty" validate:"omitempty,printascii,max=255"`
}

// GetScheme returns p's [ProxyScheme].
// It returns the zero scheme when p is nil.
func (p *Proxy) GetScheme() ProxyScheme {
	if p == nil {
		return ""
	}
	return p.Scheme
}

// SetScheme sets p's [ProxyScheme].
// It has no effect when p is nil.
func (p *Proxy) SetScheme(s ProxyScheme) {
	if p == nil {
		return
	}
	p.Scheme = s
}

// GetHost returns p's host and port.
// It returns an empty string when p is nil.
func (p *Proxy) GetHost() string {
	if p == nil {
		return ""
	}
	return p.Host
}

// SetHost sets p's host and port.
// It has no effect when p is nil.
func (p *Proxy) SetHost(s string) {
	if p == nil {
		return
	}
	p.Host = s
}

// GetUsername returns p's username.
// It returns an empty string when p is nil.
func (p *Proxy) GetUsername() string {
	if p == nil {
		return ""
	}
	return p.Username
}

// SetUsername sets p's username when s is at most 255 bytes.
// It has no effect when p is nil or s is longer than 255 bytes.
func (p *Proxy) SetUsername(s string) {
	if p == nil {
		return
	}
	if len(s) > 255 {
		return
	}
	p.Username = s
}

// GetPassword returns p's password.
// It returns an empty string when p is nil.
func (p *Proxy) GetPassword() string {
	if p == nil {
		return ""
	}
	return p.Password
}

// SetPassword sets p's password when s is at most 255 bytes.
// It has no effect when p is nil or s is longer than 255 bytes.
func (p *Proxy) SetPassword(s string) {
	if p == nil {
		return
	}
	if len(s) > 255 {
		return
	}
	p.Password = s
}

// ExportURL returns a [url.URL] containing p's scheme, host, and credentials.
// It returns nil when p is nil.
func (p *Proxy) ExportURL() *url.URL {
	if p == nil {
		return nil
	}
	u := new(url.URL)
	u.Scheme = p.Scheme.String()
	u.Host = p.Host
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u
}

// IsValidScheme reports whether p's scheme passes [IsValidScheme].
func (p *Proxy) IsValidScheme() bool {
	return p != nil && IsValidScheme(p.Scheme)
}

// IsValidHostnamePort reports whether p's host passes [IsValidHostnamePort].
func (p *Proxy) IsValidHostnamePort() bool {
	return p != nil && IsValidHostnamePort(p.Host)
}

// IsCredentialFilled reports whether p has a username.
// It reports false when p is nil.
func (p *Proxy) IsCredentialFilled() bool {
	return p != nil && p.Username != ""
}

// IsValidCredentials reports whether p's credentials pass
// [IsValidCredentials].
func (p *Proxy) IsValidCredentials() bool {
	return p != nil && IsValidCredentials(p.Username, p.Password)
}

// IsZero reports whether every field in p is empty.
func (p Proxy) IsZero() bool {
	return Proxy{} == p
}

// IsValid reports whether p passes [Proxy.IsValidScheme],
// [Proxy.IsValidHostnamePort], and [Proxy.IsValidCredentials].
func (p *Proxy) IsValid() bool {
	return p != nil &&
		p.IsValidScheme() &&
		p.IsValidHostnamePort() &&
		p.IsValidCredentials()
}

// IsValidScheme reports whether s is a supported proxy scheme.
// It returns true for [HTTP], [HTTPS], [SOCKS5], and [SOCKS5H].
func IsValidScheme(s ProxyScheme) bool {
	switch s {
	case HTTP, HTTPS, SOCKS5, SOCKS5H:
		return true
	default:
		return false
	}
}

// IsValidHostnamePort reports whether hnp is a valid host-port pair.
// The port must be between 1 and 65535. A non-empty host must be an IP
// address or an ASCII DNS host name. DNS host labels must contain only
// letters, decimal digits, and interior hyphens, and each label must be at
// most 63 bytes. A DNS host name may end with a root dot.
func IsValidHostnamePort(hnp string) bool {
	host, port, bracketed, ok := SplitHostnamePort(hnp)
	if !ok {
		return false
	}
	if len(port) == 0 || len(port) > 5 {
		return false
	}
	var portNumber int
	for i := range len(port) {
		c := port[i]
		if c < '0' || c > '9' {
			return false
		}
		portNumber = portNumber*10 + int(c-'0')
	}
	if portNumber == 0 || portNumber > 65535 {
		return false
	}
	if host == "" {
		return !bracketed
	}
	if bracketed {
		return isValidIPLiteral(host)
	}
	return IsValidHost(host)
}

// SplitHostnamePort splits hnp into host, port, bracketed, and ok without
// validating host or port.
//
// It follows the URI authority shape from RFC 3986 section 3.2.2: host and
// port are separate fields, and square brackets delimit IP-literals only.
// The bracketed result preserves that syntax so [IsValidHostnamePort] can
// enforce IPv6 literal rules from RFC 4291, RFC 5952, and RFC 9844 while
// treating port as the separate service field described by IEEE Std
// 1003.1-2024 getaddrinfo.
func SplitHostnamePort(hnp string) (string, string, bool, bool) {
	length := len(hnp)
	if length < 2 {
		return "", "", false, false
	}
	if hnp[0] == '[' {
		for i := 1; i < length; i++ {
			if hnp[i] != ']' {
				continue
			}
			if i+1 >= length || hnp[i+1] != ':' {
				return "", "", false, false
			}
			return hnp[1:i], hnp[i+2:], true, true
		}
		return "", "", false, false
	}
	for i := length - 1; i >= 0; i-- {
		if hnp[i] != ':' {
			continue
		}
		for j := range i {
			if hnp[j] == ':' {
				return "", "", false, false
			}
		}
		return hnp[:i], hnp[i+1:], false, true
	}
	return "", "", false, false
}

func isValidIPLiteral(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6()
}

// IsValidHost reports whether host is a valid IP address or ASCII DNS host
// name without brackets or port.
//
// It accepts IPv4 and IPv6 text forms parsed by [netip.ParseAddr]. DNS names
// follow the LDH host-name rules from RFC 1035 and RFC 1123, the label and
// full-name length limits from RFC 2181, and may end with a root dot. Use
// [SplitHostnamePort] or [IsValidHostnamePort] for host:port input.
func IsValidHost(host string) bool {
	length := len(host)
	if length == 0 {
		return false
	}
	if host[length-1] == '.' {
		if length == 1 || length > maxAbsoluteDNSNameLength {
			return false
		}
		host = host[:length-1]
		length--
	} else if length > maxDNSNameLength {
		return false
	}
	var labelStart int
	for i := range length {
		c := host[i]
		if c == ':' {
			_, err := netip.ParseAddr(host)
			return err == nil
		}
		if c == '.' {
			if !isValidHostLabel(host, labelStart, i) {
				return false
			}
			labelStart = i + 1
			continue
		}
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-') {
			return false
		}
	}
	return isValidHostLabel(host, labelStart, length)
}

func isValidHostLabel(host string, start, end int) bool {
	length := end - start
	return length > 0 &&
		length <= maxDNSLabelLength &&
		host[start] != '-' &&
		host[end-1] != '-'
}

// IsValidCredentials reports whether username and password form valid proxy
// credentials. Each value may contain at most 255 bytes and no ASCII control
// bytes. A non-empty password requires a non-empty username.
func IsValidCredentials(username, password string) bool {
	if username == "" && password != "" {
		return false
	}
	if len(username) > 255 || len(password) > 255 {
		return false
	}
	if stringContainsCTLByte(username) || stringContainsCTLByte(password) {
		return false
	}
	return true
}

// stringContainsCTLByte reports whether s contains an ASCII control byte.
func stringContainsCTLByte(s string) bool {
	for i, b, length := 0, byte(0), len(s); i < length; i++ {
		if b = s[i]; b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}

// Reset clears every field in p.
// It has no effect when p is nil.
func (p *Proxy) Reset() {
	if p == nil {
		return
	}
	p.Scheme = ""
	p.Host = ""
	p.Username = ""
	p.Password = ""
}
