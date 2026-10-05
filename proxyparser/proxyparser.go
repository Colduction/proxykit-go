// Package proxyparser compiles proxy formats and parses proxy strings into
// [proxykit.Proxy] values.
package proxyparser

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
)

var (
	// ErrInvalidProxyFormat is returned, wrapped together with the failing
	// [proxykit.Proxy.Validate] sentinel, when parsed fields are not a valid
	// proxy. Match either error with [errors.Is].
	ErrInvalidProxyFormat = errors.New("proxyparser: parsed string is not a valid proxy format")
	// ErrInvalidFormatEndStr is returned by [New] when format ends with an
	// incomplete verb.
	ErrInvalidFormatEndStr = errors.New("proxyparser: format string cannot end with '%'")
	// ErrFormatTooLong is returned by [New] when format is longer than
	// 2^32-1 bytes.
	ErrFormatTooLong = errors.New("proxyparser: format string is too long")
	// ErrHostNotParsed is returned when lenient parsing produces no host.
	ErrHostNotParsed = errors.New("proxyparser: could not parse host")
	// ErrSchemeNotParsed is returned when lenient parsing produces no scheme.
	ErrSchemeNotParsed = errors.New("proxyparser: could not parse scheme")
	// ErrNilProxy is returned by [Parser.ParseInto] when the destination is nil.
	ErrNilProxy = errors.New("proxyparser: nil proxy destination")
)

type (
	// ErrUnexpectedDelim records a required delimiter missing from input.
	ErrUnexpectedDelim string
	// ErrUnexpectedTrail records input remaining after strict parsing.
	ErrUnexpectedTrail string
	// ErrSubseqDelimNotFound records a delimiter missing after a parsed field.
	ErrSubseqDelimNotFound string
	// ErrInvalidFormatVerb records an unsupported verb passed to [New].
	ErrInvalidFormatVerb byte
	// ErrMismatchDelim records a delimiter mismatch during strict parsing.
	ErrMismatchDelim struct {
		delim string
		pos   int
	}
)

// Error returns a description of the missing delimiter.
func (err ErrUnexpectedDelim) Error() string {
	return fmt.Sprintf("proxyparser: expected but did not find subsequent delimiter %q", string(err))
}

// Error returns a description of the unparsed input.
func (err ErrUnexpectedTrail) Error() string {
	return fmt.Sprintf("proxyparser: unexpected trailing characters in input: %q", string(err))
}

// Error returns a description of the missing subsequent delimiter.
func (err ErrSubseqDelimNotFound) Error() string {
	return fmt.Sprintf("proxyparser: expected but did not find subsequent delimiter %q", string(err))
}

// Error returns a description of the unsupported format verb.
func (err ErrInvalidFormatVerb) Error() string {
	return fmt.Sprintf("proxyparser: invalid format verb: %%%c", byte(err))
}

// Error returns a description of the delimiter mismatch.
func (err ErrMismatchDelim) Error() string {
	return fmt.Sprintf("proxyparser: expected delimiter %q, but mismatch found at position %d", err.delim, err.pos)
}

type parserKind int8

const (
	parserGeneric parserKind = iota
	parserStrictSchemeHostPort
	parserStrictFullCredentials
)

const (
	noPlanIndex uint32 = ^uint32(0)

	noClass              = structuralindex.Class(0xff)
	schemeSeparatorClass = structuralindex.Class(0xfe)
)

type parseOp struct {
	delimiterStart   uint32
	delimiterLength  uint32
	credentialEnd    uint32
	field            byte
	delimiterByte    byte
	delimiterClass   structuralindex.Class
	consumeDelimiter bool
}

// Parser parses proxy strings according to a format compiled by [New].
// A [Parser] is safe for concurrent use by multiple goroutines.
type Parser interface {
	// Parse parses the input and returns a newly allocated [proxykit.Proxy].
	Parse(input string) (*proxykit.Proxy, error)

	// ParseString parses the input and returns a [proxykit.Proxy] by value.
	ParseString(input string) (proxykit.Proxy, error)

	// ParseInto resets the destination and parses the input into it.
	// Successful parsing does not allocate for standard proxy formats.
	// Callers must not access the destination during the call. Concurrent calls must use
	// distinct destinations. When [Parser.ParseInto] returns a non-nil error the
	// destination's contents are unspecified. Parsed fields may share the input's bytes.
	ParseInto(input string, dst *proxykit.Proxy) error

	// ParseBytes parses the input into the destination without converting the input to an
	// allocated string.
	//
	// Parsed string fields may alias the input. A scheme canonicalized by
	// [proxykit.ParseScheme] is a constant, and joining nonadjacent host and port
	// fields allocates a separate string. Concurrent calls must use distinct
	// destinations, and callers must keep the input immutable while a parsed destination is
	// in use. When [Parser.ParseBytes] returns a non-nil error the destination's contents are
	// unspecified.
	ParseBytes(input []byte, dst *proxykit.Proxy) error
}

// Parse is a parser compiled by [New]. It implements [Parser] and is safe for
// concurrent use by multiple goroutines.
type Parse struct {
	format            string
	plan              []parseOp
	strict, hasScheme bool
	kind              parserKind
}

// New compiles the format and returns a [*Parse].
//
// Format accepts %t for scheme, %h for host, %d for port, %u for username,
// %p for password, and %% for a literal percent sign.
// A bracketed IPv6 host is captured through its closing bracket before
// searching for the next delimiter.
//
// In strict mode, the input must match the format exactly, and each host and port
// capture must be nonempty.
// In lenient mode, credentials may be absent, and a %u:%p@ group also accepts
// username@ with an empty password. Delimiter mismatches and trailing input
// are ignored, and the final proxy must still pass [proxykit.Proxy.IsValid].
func New(format string, strict bool) (*Parse, error) {
	if uint64(len(format)) > uint64(noPlanIndex) {
		return nil, ErrFormatTooLong
	}
	kind := detectParserKind(format, strict)
	if kind != parserGeneric {
		return &Parse{strict: true, hasScheme: true, kind: kind}, nil
	}
	count, err := countFormatOps(format)
	if err != nil {
		return nil, err
	}
	plan := make([]parseOp, 0, count)
	var hasScheme bool
	for i := 0; i < len(format); {
		if format[i] != '%' {
			start := i
			for i < len(format) && format[i] != '%' {
				i++
			}
			plan = append(plan, parseOp{
				delimiterStart:  uint32(start),
				delimiterLength: uint32(i - start),
				delimiterClass:  noClass,
			})
			op := &plan[len(plan)-1]
			if i-start == 1 {
				op.delimiterByte = format[start]
				if class, ok := structuralindex.ClassOf(format[start]); ok {
					op.delimiterClass = class
				}
			} else if format[start:i] == schemeSeparator {
				op.delimiterClass = schemeSeparatorClass
			}
			continue
		}
		verb := format[i+1]
		if verb == '%' {
			plan = append(plan, parseOp{
				delimiterStart:  uint32(i),
				delimiterLength: 1,
				delimiterByte:   '%',
				delimiterClass:  noClass,
			})
		} else {
			plan = append(plan, parseOp{
				credentialEnd: noPlanIndex,
				field:         verb,
			})
			if verb == 't' {
				hasScheme = true
			}
		}
		i += 2
	}
	var (
		nextDelimiterByte                       byte
		nextDelimiterStart, nextDelimiterLength uint32
	)
	nextDelimiterClass := noClass
	nextDelimiter, credentialEnd := noPlanIndex, noPlanIndex
	for i := len(plan) - 1; i >= 0; i-- {
		op := &plan[i]
		if op.field == 0 {
			nextDelimiter = uint32(i)
			nextDelimiterStart = op.delimiterStart
			nextDelimiterLength = op.delimiterLength
			nextDelimiterByte = op.delimiterByte
			nextDelimiterClass = op.delimiterClass
			if op.delimiterLength == 1 && op.delimiterByte == '@' {
				credentialEnd = uint32(i)
			}
			continue
		}
		op.delimiterStart = nextDelimiterStart
		op.delimiterLength = nextDelimiterLength
		op.delimiterByte = nextDelimiterByte
		op.delimiterClass = nextDelimiterClass
		op.credentialEnd = credentialEnd
		op.consumeDelimiter = nextDelimiter == uint32(i+1)
	}
	return &Parse{
		plan:      plan,
		format:    format,
		strict:    strict,
		hasScheme: hasScheme,
		kind:      kind,
	}, nil
}

// Parse implements [Parser.Parse]. A nil receiver returns a nil proxy and error.
func (pp *Parse) Parse(input string) (*proxykit.Proxy, error) {
	if pp == nil {
		return nil, nil
	}
	var parsed proxykit.Proxy
	if err := pp.parseInto(input, &parsed, true); err != nil {
		return nil, err
	}
	return new(parsed), nil
}

// ParseString implements [Parser.ParseString]. A nil receiver returns a zero proxy and nil error.
func (pp *Parse) ParseString(input string) (proxy proxykit.Proxy, err error) {
	if pp != nil {
		err = pp.parseInto(input, &proxy, true)
	}
	return proxy, err
}

// ParseBytes implements [Parser.ParseBytes] with the nil behavior of [Parse.ParseInto].
func (pp *Parse) ParseBytes(input []byte, proxy *proxykit.Proxy) error {
	return pp.ParseInto(unsafe.String(unsafe.SliceData(input), len(input)), proxy)
}

// ParseInto implements [Parser.ParseInto]. A nil receiver leaves the destination unchanged
// and returns nil. Otherwise a nil destination returns [ErrNilProxy].
func (pp *Parse) ParseInto(input string, proxy *proxykit.Proxy) error {
	if pp == nil {
		return nil
	}
	if proxy == nil {
		return ErrNilProxy
	}
	return pp.parseInto(input, proxy, true)
}

func (pp *Parse) parseInto(input string, proxy *proxykit.Proxy, fast bool) error {
	switch pp.kind {
	case parserStrictSchemeHostPort:
		if fast {
			if handled, err := parseStrictFast(input, proxy, false); handled {
				return err
			}
		}
		scheme, host, err := parseStrictSchemeHostPort(input)
		*proxy = proxykit.Proxy{Scheme: scheme, Host: host}
		return err
	case parserStrictFullCredentials:
		if fast {
			if handled, err := parseStrictFast(input, proxy, true); handled {
				return err
			}
		}
		scheme, host, username, password, err := parseStrictFullCredentials(input)
		*proxy = proxykit.Proxy{Scheme: scheme, Host: host, Username: username, Password: password}
		return err
	}
	proxy.Reset()
	var ix structuralindex.Index
	indexed := fast && ix.Build(input)
	var (
		inputPos     int
		hostStart    int = -1
		hostEnd      int
		hostParsed   bool
		schemeParsed bool
		emptyHost    bool
		emptyPort    bool
	)
	if !pp.strict && !pp.hasScheme {
		idx := indexSchemeSeparator(&ix, indexed, input, 0)
		if idx < 0 {
			return ErrSchemeNotParsed
		}
		proxy.Scheme = proxykit.ProxyScheme(input[:idx])
		if !proxy.Scheme.IsValid() {
			proxy.Scheme, _ = schemeOf(input[:idx])
		}
		schemeParsed = proxy.Scheme != ""
		inputPos = idx + 3
	}
	format := pp.format
	for i, plan := 0, pp.plan; i < len(plan); i++ {
		op := &plan[i]
		if op.field == 0 {
			delimiterLength := int(op.delimiterLength)
			matched := delimiterLength <= len(input)-inputPos
			if matched {
				if delimiterLength == 1 {
					matched = input[inputPos] == op.delimiterByte
				} else {
					delimiterStart := int(op.delimiterStart)
					delimiter := format[delimiterStart : delimiterStart+delimiterLength]
					matched = input[inputPos:inputPos+delimiterLength] == delimiter
				}
			}
			if matched {
				inputPos += delimiterLength
			}
			if !matched && pp.strict {
				delimiterStart := int(op.delimiterStart)
				delimiter := format[delimiterStart : delimiterStart+delimiterLength]
				return &ErrMismatchDelim{delimiter, inputPos}
			}
			continue
		}
		start := inputPos
		delimiterLength := int(op.delimiterLength)
		var nextDelimiter string
		credentialAt := -1
		if !pp.strict && (op.field == 'u' || op.field == 'p') && op.credentialEnd != noPlanIndex {
			credentialAt = indexByteFrom(&ix, indexed, input, inputPos, '@', structuralindex.At)
			if credentialAt < 0 {
				i = int(op.credentialEnd)
				continue
			}
		}
		idx := -1
		if delimiterLength != 0 {
			if op.field == 'h' && start < len(input) && input[start] == '[' {
				tail := input[start:]
				skip := strings.IndexByte(tail, ']') + 1
				delimiterStart := int(op.delimiterStart)
				nextDelimiter = format[delimiterStart : delimiterStart+delimiterLength]
				idx = strings.Index(tail[skip:], nextDelimiter)
				if idx >= 0 {
					idx += skip
				}
			} else if delimiterLength == 1 {
				idx = indexByteFrom(&ix, indexed, input, start, op.delimiterByte, op.delimiterClass)
			} else {
				delimiterStart := int(op.delimiterStart)
				nextDelimiter = format[delimiterStart : delimiterStart+delimiterLength]
				if op.delimiterClass == schemeSeparatorClass {
					idx = indexSchemeSeparator(&ix, indexed, input, start)
				} else {
					idx = strings.Index(input[start:], nextDelimiter)
				}
			}
		}
		if credentialAt >= 0 && op.field == 'u' && op.delimiterByte == ':' &&
			op.credentialEnd == uint32(i+3) && plan[i+2].field == 'p' && idx > credentialAt &&
			indexByteFrom(&ix, indexed, input, start+idx+1, '@', structuralindex.At) < 0 {
			proxy.Username, proxy.Password = input[start:start+credentialAt], ""
			inputPos = start + credentialAt + 1
			i = int(op.credentialEnd)
			continue
		}
		if !pp.strict && delimiterLength != 0 && idx == -1 {
			switch op.field {
			case 'h':
				assignField(proxy, 'h', input[start:])
				hostStart = start
				hostEnd = len(input)
				hostParsed = hostParsed || start < len(input)
				inputPos = len(input)
			case 'u':
				if at := indexByteFrom(&ix, indexed, input, start, '@', structuralindex.At); at >= 0 {
					assignField(proxy, 'u', input[start:start+at])
					inputPos = start + at
				} else if delimiterLength == 1 && op.delimiterByte == ':' {
					assignField(proxy, 'u', input[start:])
					inputPos = len(input)
				}
			case 'p':
				if at := indexByteFrom(&ix, indexed, input, start, '@', structuralindex.At); at == 0 {
					assignField(proxy, 'p', "")
				} else if at > 0 {
					assignField(proxy, 'p', input[start:start+at])
					inputPos = start + at
				}
			default:
				assignField(proxy, op.field, input[start:])
				inputPos = len(input)
			}
			continue
		}
		if delimiterLength != 0 {
			if idx == -1 {
				if nextDelimiter == "" {
					delimiterStart := int(op.delimiterStart)
					nextDelimiter = format[delimiterStart : delimiterStart+delimiterLength]
				}
				return ErrSubseqDelimNotFound(nextDelimiter)
			}
			inputPos = start + idx
		} else {
			inputPos = len(input)
		}
		capturedValue := input[start:inputPos]
		switch op.field {
		case 'h':
			if capturedValue == "" {
				emptyHost = true
			}
			proxy.Host = capturedValue
			hostStart = start
			hostEnd = inputPos
			hostParsed = capturedValue != ""
		case 'd':
			if capturedValue == "" {
				emptyPort = true
			}
			if hostStart >= 0 && hostEnd+1 == start && input[hostEnd] == ':' {
				proxy.Host = input[hostStart:inputPos]
			} else if proxy.Host == "" {
				proxy.Host = capturedValue
			} else if capturedValue != "" {
				proxy.Host += ":" + capturedValue
			}
		default:
			assignField(proxy, op.field, capturedValue)
			if op.field == 't' && capturedValue != "" {
				schemeParsed = true
			}
		}
		if op.consumeDelimiter {
			inputPos += delimiterLength
			i++
		}
	}
	if pp.strict {
		if inputPos < len(input) {
			return ErrUnexpectedTrail(input[inputPos:])
		}
		if emptyHost || emptyPort {
			if !proxy.Scheme.IsValid() {
				return errInvalidScheme
			}
			if proxy.Host == "" {
				return errInvalidHost
			}
			if emptyPort {
				return errInvalidPort
			}
			return errInvalidHost
		}
	} else {
		if !schemeParsed && proxy.Scheme == "" {
			return ErrSchemeNotParsed
		}
		if !hostParsed || proxy.Host == "" {
			return ErrHostNotParsed
		}
	}
	if fast && isValidFast(&ix, indexed, input, proxy) {
		return nil
	}
	if err := proxy.Validate(); err != nil {
		return invalidProxyFormat(err)
	}
	return nil
}

func parseStrictSchemeHostPort(input string) (proxykit.ProxyScheme, string, error) {
	before, after, ok := strings.Cut(input, "://")
	if !ok {
		return "", "", ErrSubseqDelimNotFound("://")
	}
	scheme := proxykit.ProxyScheme(before)
	if !scheme.IsValid() {
		scheme, _ = schemeOf(before)
	}
	parsed := proxykit.Proxy{Scheme: scheme, Host: after}
	if err := parsed.Validate(); err != nil {
		if strings.LastIndexByte(after, ':') < 0 {
			return "", "", ErrSubseqDelimNotFound(":")
		}
		return scheme, after, invalidProxyFormat(err)
	}
	return scheme, after, nil
}

func parseStrictFullCredentials(input string) (proxykit.ProxyScheme, string, string, string, error) {
	schemeEnd := strings.Index(input, "://")
	if schemeEnd < 0 {
		return "", "", "", "", ErrSubseqDelimNotFound("://")
	}
	usernameStart := schemeEnd + 3
	usernameEnd := strings.IndexByte(input[usernameStart:], ':')
	if usernameEnd < 0 {
		return "", "", "", "", ErrSubseqDelimNotFound(":")
	}
	usernameEnd += usernameStart
	passwordStart := usernameEnd + 1
	passwordEnd := strings.IndexByte(input[passwordStart:], '@')
	if passwordEnd < 0 {
		return "", "", "", "", ErrSubseqDelimNotFound("@")
	}
	passwordEnd += passwordStart
	host := input[passwordEnd+1:]
	scheme := proxykit.ProxyScheme(input[:schemeEnd])
	if !scheme.IsValid() {
		scheme, _ = schemeOf(input[:schemeEnd])
	}
	username, password := input[usernameStart:usernameEnd], input[passwordStart:passwordEnd]
	parsed := proxykit.Proxy{Scheme: scheme, Host: host, Username: username, Password: password}
	if err := parsed.Validate(); err != nil {
		if strings.LastIndexByte(host, ':') < 0 {
			return "", "", "", "", ErrSubseqDelimNotFound(":")
		}
		return scheme, host, username, password, invalidProxyFormat(err)
	}
	return scheme, host, username, password, nil
}

func assignField(proxy *proxykit.Proxy, field byte, val string) {
	switch field {
	case 't':
		proxy.Scheme = proxykit.ProxyScheme(val)
		if !proxy.Scheme.IsValid() {
			proxy.Scheme, _ = schemeOf(val)
		}
	case 'h', 'd':
		if proxy.Host == "" {
			proxy.Host = val
		} else if val != "" {
			proxy.Host += ":" + val
		}
	case 'u':
		proxy.Username = val
	case 'p':
		proxy.Password = val
	}
}

func schemeOf(s string) (proxykit.ProxyScheme, bool) {
	if scheme, ok := proxykit.ParseScheme(s); ok {
		return scheme, true
	}
	return proxykit.ProxyScheme(s), false
}

var (
	errInvalidScheme      = fmt.Errorf("%w: %w", ErrInvalidProxyFormat, proxykit.ErrInvalidScheme)
	errInvalidHost        = fmt.Errorf("%w: %w", ErrInvalidProxyFormat, proxykit.ErrInvalidHost)
	errInvalidPort        = fmt.Errorf("%w: %w", ErrInvalidProxyFormat, proxykit.ErrInvalidPort)
	errInvalidCredentials = fmt.Errorf("%w: %w", ErrInvalidProxyFormat, proxykit.ErrInvalidCredentials)
)

func invalidProxyFormat(err error) error {
	switch err {
	case proxykit.ErrInvalidScheme:
		return errInvalidScheme
	case proxykit.ErrInvalidHost:
		return errInvalidHost
	case proxykit.ErrInvalidPort:
		return errInvalidPort
	case proxykit.ErrInvalidCredentials:
		return errInvalidCredentials
	default:
		return ErrInvalidProxyFormat
	}
}

func countFormatOps(format string) (int, error) {
	var count int
	for i := 0; i < len(format); i++ {
		count++
		if format[i] == '%' {
			i++
			if i == len(format) {
				return 0, ErrInvalidFormatEndStr
			}
			switch verb := format[i]; verb {
			case 't', 'h', 'd', 'u', 'p', '%':
			default:
				return 0, ErrInvalidFormatVerb(verb)
			}
			continue
		}
		for i+1 < len(format) && format[i+1] != '%' {
			i++
		}
	}
	return count, nil
}

func detectParserKind(format string, strict bool) parserKind {
	if !strict {
		return parserGeneric
	}
	switch format {
	case "%t://%h:%d":
		return parserStrictSchemeHostPort
	case "%t://%u:%p@%h:%d":
		return parserStrictFullCredentials
	default:
		return parserGeneric
	}
}
