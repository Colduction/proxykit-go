// Package proxykit defines proxy addresses, schemes, credentials, and
// validation helpers.
//
// Validators run in O(len(input)) time and do not allocate for accepted
// input. Host names must be ASCII: convert
// internationalized names to A-labels, for example with
// golang.org/x/net/idna, before validation.
package proxykit

import (
	"errors"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Validation errors returned by [Proxy.Validate] and [FromURL].
// They are sentinels; match them with [errors.Is].
var (
	// ErrNilProxy reports a nil *Proxy.
	ErrNilProxy = errors.New("proxykit: nil proxy")
	// ErrNilURL reports a nil *url.URL passed to [FromURL].
	ErrNilURL = errors.New("proxykit: nil URL")
	// ErrInvalidProxyURL reports a URL with a path, query, fragment, or opaque
	// part, none of which a proxy endpoint has.
	ErrInvalidProxyURL = errors.New("proxykit: URL is not a proxy endpoint")
	// ErrInvalidScheme reports a scheme that [ProxyScheme.IsValid] rejects.
	ErrInvalidScheme = errors.New("proxykit: invalid or unsupported scheme")
	// ErrInvalidHost reports an empty Host, an empty host part beside a port
	// (":80"), an unbracketed IPv6 address, an unterminated IP-literal
	// bracket, or a name or literal that [IsValidHost] rejects.
	ErrInvalidHost = errors.New("proxykit: invalid host")
	// ErrInvalidPort reports a missing, empty, non-numeric, or out-of-range
	// port. A non-empty Host with no port separator, such as "host" or
	// "[::1]", is reported here.
	ErrInvalidPort = errors.New("proxykit: invalid port")
	// ErrInvalidCredentials reports credentials that [IsValidCredentialsFor]
	// rejects.
	ErrInvalidCredentials = errors.New("proxykit: invalid credentials")
)

// ProxyScheme identifies a proxy protocol in canonical lowercase form.
// [ParseScheme] canonicalizes user input, and [ProxyScheme.IsValid] reports
// whether a value is one of the supported constants.
type ProxyScheme string

// String returns the string representation of s.
func (s ProxyScheme) String() string {
	return string(s)
}

// IsValid reports whether s is one of [HTTP], [HTTPS], [SOCKS4], [SOCKS4A],
// [SOCKS5], and [SOCKS5H] in canonical lowercase form.
func (s ProxyScheme) IsValid() bool {
	switch s {
	case HTTP, HTTPS, SOCKS4, SOCKS4A, SOCKS5, SOCKS5H:
		return true
	default:
		return false
	}
}

// DefaultPort returns the conventional port for s as net/http and the IANA
// registry assign it: 80 for [HTTP], 443 for [HTTPS], and 1080 for the SOCKS
// schemes. It returns 0 for an unsupported scheme. curl instead defaults
// every proxy port except https to 1080.
func (s ProxyScheme) DefaultPort() uint16 {
	switch s {
	case HTTP:
		return 80
	case HTTPS:
		return 443
	case SOCKS4, SOCKS4A, SOCKS5, SOCKS5H:
		return 1080
	default:
		return 0
	}
}

const (
	// HTTP is the [ProxyScheme] for an HTTP proxy reached over cleartext TCP.
	// Clients send absolute-form requests (RFC 9112 section 3.2.2) or CONNECT
	// (RFC 9110 section 9.3.6), so the proxy resolves host names.
	HTTP ProxyScheme = "http"
	// HTTPS is the [ProxyScheme] for an HTTP proxy reached over TLS, the
	// meaning curl, Chromium, and net/http give the https proxy scheme. It
	// says nothing about the target scheme. No RFC or major client defines a
	// SOCKS-over-TLS scheme, so none exists here.
	HTTPS ProxyScheme = "https"
	// SOCKS4 is the [ProxyScheme] for a SOCKS version 4 proxy: IPv4
	// destinations only, a USERID and no password, and the client resolves
	// host names.
	SOCKS4 ProxyScheme = "socks4"
	// SOCKS4A is the [ProxyScheme] for SOCKS4 with the 4A extension, under
	// which the proxy resolves host names.
	SOCKS4A ProxyScheme = "socks4a"
	// SOCKS5 is the [ProxyScheme] for a SOCKS5 proxy (RFC 1928) at which curl
	// resolves host names locally. net/http and golang.org/x/net/proxy treat
	// it as [SOCKS5H].
	SOCKS5 ProxyScheme = "socks5"
	// SOCKS5H is the [ProxyScheme] for a SOCKS5 proxy that resolves host names
	// remotely, following the curl convention.
	SOCKS5H ProxyScheme = "socks5h"
)

// ParseScheme returns the canonical [ProxyScheme] for s, folding ASCII letters
// to lowercase as RFC 3986 section 3.1 permits. It reports false for an
// unsupported scheme, accepts no aliases, and does not allocate. When s is
// already canonical the result shares s's bytes.
func ParseScheme(s string) (ProxyScheme, bool) {
	if scheme := ProxyScheme(s); scheme.IsValid() {
		return scheme, true
	}
	var buf [len(SOCKS5H)]byte
	if len(s) == 0 || len(s) > len(buf) {
		return "", false
	}
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		buf[i] = c
	}
	switch string(buf[:len(s)]) {
	case "http":
		return HTTP, true
	case "https":
		return HTTPS, true
	case "socks4":
		return SOCKS4, true
	case "socks4a":
		return SOCKS4A, true
	case "socks5":
		return SOCKS5, true
	case "socks5h":
		return SOCKS5H, true
	default:
		return "", false
	}
}

// DNS presentation lengths derive from the 255-octet wire name limit,
// including label length octets and the root label.
const (
	maxDNSLabelLength        = 63
	maxDNSNameLength         = 253
	maxAbsoluteDNSNameLength = 254
)

// maxCredentialLength is the RFC 1929 section 2 limit on SOCKS5 user names
// and passwords, applied to every scheme.
const maxCredentialLength = 255

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
	SetScheme(s ProxyScheme)

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

	// Validate returns nil when the proxy fields are valid and otherwise the
	// first validation error.
	Validate() error
}

// ProxyResetter describes a proxy reset operation.
type ProxyResetter interface {
	// Reset clears every proxy field.
	Reset()
}

// Proxy represents a proxy endpoint and its optional credentials.
//
// The validate struct tags are a best-effort subset for tag-driven
// validators; [Proxy.Validate] is authoritative. Every encoder tag serializes
// Password in plaintext. A Proxy or *Proxy passed to log/slog as an attribute
// value is redacted by [Proxy.LogValue]; a slice or struct field holding one
// is formatted by the handler and prints the password. Elsewhere use
// [Proxy.ExportURL] followed by [url.URL.Redacted].
type Proxy struct {
	// Scheme is the proxy protocol in canonical lowercase form.
	Scheme ProxyScheme `json:"scheme,omitempty" yaml:"scheme,omitempty" xml:"scheme,omitempty" cbor:"scheme,omitempty" bson:"scheme,omitempty" msgpack:"scheme,omitempty" toml:"scheme,omitempty" mapstructure:"scheme,omitempty" validate:"required_with=Host,omitempty,oneof=http https socks4 socks4a socks5 socks5h"`

	// Host is the endpoint as host:port or [ipv6]:port, the form accepted by
	// net.Dial. An IPv6 zone ID uses the raw RFC 4007 form "[fe80::1%eth0]:port";
	// [Proxy.ExportURL] copies it unchanged into [url.URL.Host], and
	// [url.URL.String] percent-encodes it.
	Host string `json:"host,omitempty" yaml:"host,omitempty" xml:"host,omitempty" cbor:"host,omitempty" bson:"host,omitempty" msgpack:"host,omitempty" toml:"host,omitempty" mapstructure:"host,omitempty" validate:"required_with=Scheme,omitempty"`

	// Username is the optional raw proxy username.
	Username string `json:"username,omitempty" yaml:"username,omitempty" xml:"username,omitempty" cbor:"username,omitempty" bson:"username,omitempty" msgpack:"username,omitempty" toml:"username,omitempty" mapstructure:"username,omitempty" validate:"required_with_all=Scheme Host Password,omitempty,printascii,max=255"`

	// Password is the optional raw proxy password. Every encoder tag serializes
	// it in plaintext.
	Password string `json:"password,omitempty" yaml:"password,omitempty" xml:"password,omitempty" cbor:"password,omitempty" bson:"password,omitempty" msgpack:"password,omitempty" toml:"password,omitempty" mapstructure:"password,omitempty" validate:"omitempty,printascii,max=255"`
}

// FromURL converts u, typically from [url.Parse] or
// [net/http.ProxyFromEnvironment], into a validated Proxy. It is the inverse
// of [Proxy.ExportURL]: the scheme is canonicalized with [ParseScheme] and
// defaults to [HTTP] when empty, a missing or empty port becomes
// [ProxyScheme.DefaultPort] as net/http treats it, and credentials are taken
// percent-decoded from u.User. Only an empty or "/" path is tolerated; a
// query, fragment, or opaque part returns [ErrInvalidProxyURL]. A bare
// "host:port" string is not a URL: [url.Parse] reads a name as the scheme with
// the port as the opaque part and rejects an IP literal outright, so prefix
// it with "http://" or use proxyparser instead.
//
// FromURL is a convenience for configuration rather than the hot-path parser.
// It returns the zero Proxy with [ErrNilURL] when u is nil,
// [ErrInvalidProxyURL] as described above, or the first [Proxy.Validate]
// sentinel when u does not describe a usable endpoint.
func FromURL(u *url.URL) (Proxy, error) {
	if u == nil {
		return Proxy{}, ErrNilURL
	}
	if u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return Proxy{}, ErrInvalidProxyURL
	}
	scheme := HTTP
	if u.Scheme != "" {
		var ok bool
		if scheme, ok = ParseScheme(u.Scheme); !ok {
			return Proxy{}, ErrInvalidScheme
		}
	}
	host := u.Host
	if host == "" {
		return Proxy{}, ErrInvalidHost
	}
	if _, port, _, ok := SplitHostnamePort(host); !ok || port == "" {
		host = strings.TrimSuffix(host, ":") + ":" + strconv.Itoa(int(scheme.DefaultPort()))
	}
	p := Proxy{Scheme: scheme, Host: host}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	if err := p.Validate(); err != nil {
		return Proxy{}, err
	}
	return p, nil
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

// SetUsername sets p's username without validating it; [Proxy.Validate] and
// [Proxy.IsValidCredentials] are the gates.
// It has no effect when p is nil.
func (p *Proxy) SetUsername(s string) {
	if p == nil {
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

// SetPassword sets p's password without validating it; [Proxy.Validate] and
// [Proxy.IsValidCredentials] are the gates.
// It has no effect when p is nil.
func (p *Proxy) SetPassword(s string) {
	if p == nil {
		return
	}
	p.Password = s
}

// ExportURL returns a [url.URL] containing p's scheme, host, and credentials.
// A username with an empty password is exported without a password, so the
// URL renders as user@host rather than user:@host; a password without a
// username is not exported, since [IsValidCredentials] rejects that state.
// [url.URL.String] percent-encodes credentials and IPv6 zone IDs, so recover
// them with [url.Parse] rather than by splitting the string.
// It returns nil when p is nil.
func (p *Proxy) ExportURL() *url.URL {
	if p == nil {
		return nil
	}
	u := new(url.URL)
	u.Scheme = p.Scheme.String()
	u.Host = p.Host
	if p.Username != "" {
		if p.Password != "" {
			u.User = url.UserPassword(p.Username, p.Password)
		} else {
			u.User = url.User(p.Username)
		}
	}
	return u
}

// LogValue implements [slog.LogValuer]. It renders p as a URL with any
// password replaced by "xxxxx", as [url.URL.Redacted] does, so a Proxy passed
// to slog as an attribute value, by value or by pointer, never reveals its
// password. It has a value receiver, unlike the other methods, so that both
// forms are redacted; slog reports a nil *Proxy as a LogValue panic rather
// than dereferencing it.
func (p Proxy) LogValue() slog.Value {
	return slog.StringValue(p.ExportURL().Redacted())
}

// IsValidScheme reports whether p's scheme passes [ProxyScheme.IsValid].
func (p *Proxy) IsValidScheme() bool {
	return p != nil && p.Scheme.IsValid()
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
// [IsValidCredentialsFor] with p's scheme.
func (p *Proxy) IsValidCredentials() bool {
	return p != nil && IsValidCredentialsFor(p.Scheme, p.Username, p.Password)
}

// IsZero reports whether every field in p is empty.
// It reports true when p is nil. IsZero has a pointer receiver, so call it on
// an addressable Proxy or a *Proxy.
func (p *Proxy) IsZero() bool {
	return p == nil || *p == Proxy{}
}

// Validate returns nil when p describes a usable proxy endpoint, or the first
// failure in field order: [ErrNilProxy], [ErrInvalidScheme], then for the Host
// field [ErrInvalidHost] when it is empty and otherwise [ErrInvalidPort]
// before [ErrInvalidHost], then [ErrInvalidCredentials]. The results are
// package sentinels; match them with [errors.Is]. Validate does not allocate
// except when rejecting a malformed bracketed IPv6 literal, where
// [netip.ParseAddr] allocates its error before it is mapped to
// [ErrInvalidHost]. IPv6 zone validation does not intern zone IDs.
func (p *Proxy) Validate() error {
	if p == nil {
		return ErrNilProxy
	}
	if !p.Scheme.IsValid() {
		return ErrInvalidScheme
	}
	if err := validateHostnamePort(p.Host); err != nil {
		return err
	}
	if !IsValidCredentialsFor(p.Scheme, p.Username, p.Password) {
		return ErrInvalidCredentials
	}
	return nil
}

// IsValid reports whether [Proxy.Validate] returns nil.
func (p *Proxy) IsValid() bool {
	return p.Validate() == nil
}

// IsValidScheme reports whether s passes [ProxyScheme.IsValid].
func IsValidScheme(s ProxyScheme) bool {
	return s.IsValid()
}

// IsValidHostnamePort reports whether hnp is a valid host-port pair.
// The port is one or more decimal digits with a value from 1 to 65535;
// leading zeros are permitted because RFC 3986 section 3.2.3 defines port as
// a digit sequence. The host must be an IP address or an ASCII DNS host name
// as [IsValidHost] defines them, an IPv6 address must be bracketed, and an
// empty host is rejected because it does not name a proxy endpoint.
func IsValidHostnamePort(hnp string) bool {
	return validateHostnamePort(hnp) == nil
}

func validateHostnamePort(hnp string) error {
	if hnp == "" {
		return ErrInvalidHost
	}
	host, port, bracketed, ok := SplitHostnamePort(hnp)
	if !ok {
		lastColon := strings.LastIndexByte(hnp, ':')
		if lastColon < 0 || (hnp[0] == '[' && strings.IndexByte(hnp, ']') > lastColon) {
			return ErrInvalidPort
		}
		return ErrInvalidHost
	}
	if !isValidPort(port) {
		return ErrInvalidPort
	}
	if host == "" {
		return ErrInvalidHost
	}
	if bracketed {
		if !isValidIPLiteral(host) {
			return ErrInvalidHost
		}
		return nil
	}
	if !IsValidHost(host) {
		return ErrInvalidHost
	}
	return nil
}

// isValidPort reports whether port is a decimal digit sequence whose value is
// from 1 to 65535. Leading zeros are stripped first so that the remaining
// digits fit the five-byte bound of the maximum port.
func isValidPort(port string) bool {
	for len(port) > 1 && port[0] == '0' {
		port = port[1:]
	}
	if len(port) == 0 || len(port) > 5 {
		return false
	}
	var value int
	for i := range len(port) {
		c := port[i]
		if c < '0' || c > '9' {
			return false
		}
		value = value*10 + int(c-'0')
	}
	return value != 0 && value <= 65535
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
func SplitHostnamePort(hnp string) (host, port string, bracketed, ok bool) {
	length := len(hnp)
	if length < 2 {
		return "", "", false, false
	}
	if hnp[0] == '[' {
		end := strings.IndexByte(hnp, ']')
		if end < 0 || end+1 >= length || hnp[end+1] != ':' {
			return "", "", false, false
		}
		return hnp[1:end], hnp[end+2:], true, true
	}
	colon := strings.IndexByte(hnp, ':')
	if colon < 0 || strings.IndexByte(hnp[colon+1:], ':') >= 0 {
		return "", "", false, false
	}
	return hnp[:colon], hnp[colon+1:], false, true
}

func isValidIPLiteral(host string) bool {
	if strings.IndexByte(host, ':') < 0 {
		return false
	}
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		if zone == len(host)-1 || !isValidZone(host[zone+1:]) {
			return false
		}
		host = host[:zone]
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6()
}

// isValidZone reports whether zone contains only RFC 3986 unreserved bytes,
// the ZoneID form that RFC 6874 section 2 allows in URIs without
// percent-encoding. An empty zone is valid.
func isValidZone(zone string) bool {
	for i := range len(zone) {
		c := zone[i]
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}

// IsValidHost reports whether host is a valid IP address or ASCII DNS host
// name without brackets or port.
//
// IPv6 text forms are parsed by [netip.ParseAddr]; a zone ID uses the raw
// RFC 4007 form and may contain only RFC 3986 unreserved characters, the
// ZoneID form RFC 6874 section 2 allows in URIs without percent-encoding.
// DNS length limits do not apply to zone IDs, and a final dot is part of the zone.
// A bare IPv6 address is valid here, but [IsValidHostnamePort] requires it
// bracketed once a port is attached. A name
// whose final label is all digits must be a dotted-decimal IPv4 address of
// four octets without leading zeros, the form [netip.ParseAddr] accepts,
// since RFC 1123 section 2.1 reserves that shape for IPv4 literals. Other
// names follow the LDH host-name rules from RFC 1035 and RFC 1123, the label
// and full-name length limits from RFC 2181, and may end with a root dot; an
// IP address may not. Underscores are rejected under those rules even though
// Go's resolver accepts them. Use [SplitHostnamePort] or [IsValidHostnamePort]
// for host:port input.
func IsValidHost(host string) bool {
	length := len(host)
	if length == 0 {
		return false
	}
	if strings.IndexByte(host, ':') >= 0 {
		return isValidIPLiteral(host)
	}
	var rootDot bool
	if host[length-1] == '.' {
		if length == 1 || length > maxAbsoluteDNSNameLength {
			return false
		}
		host = host[:length-1]
		length--
		rootDot = true
	} else if length > maxDNSNameLength {
		return false
	}
	var labelStart int
	for i := range length {
		c := host[i]
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
	if isDigitLabel(host, labelStart, length) {
		return !rootDot && isIPv4Literal(host)
	}
	return isValidHostLabel(host, labelStart, length)
}

// isDigitLabel reports whether host[start:end] is a non-empty run of ASCII
// digits.
func isDigitLabel(host string, start, end int) bool {
	if start >= end {
		return false
	}
	for i := start; i < end; i++ {
		if c := host[i]; c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isIPv4Literal reports whether host is a dotted-decimal IPv4 address in the
// form [netip.ParseAddr] accepts: four decimal octets of one to three digits,
// no leading zeros, each at most 255.
func isIPv4Literal(host string) bool {
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
		if c < '0' || c > '9' {
			return false
		}
		if digits == 1 && value == 0 {
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

func isValidHostLabel(host string, start, end int) bool {
	length := end - start
	return length > 0 &&
		length <= maxDNSLabelLength &&
		host[start] != '-' &&
		host[end-1] != '-'
}

// IsValidCredentials reports whether username and password form valid proxy
// credentials under the scheme-independent rules: each value holds at most
// 255 bytes of printable ASCII (0x20 to 0x7E), matching the printascii struct
// tag, and a non-empty password requires a non-empty username. Use
// [IsValidCredentialsFor] to apply a scheme's additional rules.
func IsValidCredentials(username, password string) bool {
	if username == "" && password != "" {
		return false
	}
	if len(username) > maxCredentialLength || len(password) > maxCredentialLength {
		return false
	}
	return isPrintableASCII(username) && isPrintableASCII(password)
}

// IsValidCredentialsFor reports whether username and password are usable with
// scheme. It applies [IsValidCredentials] and then, for [HTTP] and [HTTPS],
// rejects a colon in username because Basic authentication (RFC 7617
// section 2) joins user-id and password with one; for [SOCKS4] and [SOCKS4A]
// it requires an empty password, since the protocol carries only a USERID.
// For [SOCKS5] and [SOCKS5H] the 255-byte limits are those of RFC 1929, and an
// empty password is tolerated because curl and net/http send one. Other
// schemes get only the scheme-independent rules.
func IsValidCredentialsFor(scheme ProxyScheme, username, password string) bool {
	if !IsValidCredentials(username, password) {
		return false
	}
	switch scheme {
	case HTTP, HTTPS:
		return strings.IndexByte(username, ':') < 0
	case SOCKS4, SOCKS4A:
		return password == ""
	default:
		return true
	}
}

// isPrintableASCII reports whether every byte of s is in 0x20 to 0x7E.
func isPrintableASCII(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// Reset clears every field in p by dropping references. Go strings are
// immutable, so the underlying bytes, which may alias parser input, are not
// overwritten.
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
