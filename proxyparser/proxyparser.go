// Package proxyparser compiles proxy formats and parses proxy strings into
// [proxykit.Proxy] values.
package proxyparser

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/colduction/proxykit-go"
)

var (
	// ErrInvalidProxyFormat is returned when parsed fields fail
	// [proxykit.Proxy.IsValid].
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
	// ErrNilProxy is returned by [ProxyParser.ParseInto] when dst is nil.
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

const noPlanIndex uint32 = ^uint32(0)

// parseOp stores pointer-free offsets into proxyParser.format. A zero field
// marks a delimiter state; any other field marks a capture state.
type parseOp struct {
	delimiterStart   uint32
	delimiterLength  uint32
	credentialEnd    uint32
	field            byte
	delimiterByte    byte
	consumeDelimiter bool
}

// Parser parses proxy strings according to a format compiled by [New].
// A [Parser] is safe for concurrent use by multiple goroutines.
type Parser interface {
	// Parse parses input and returns a newly allocated [proxykit.Proxy].
	Parse(input string) (*proxykit.Proxy, error)

	// ParseString parses input and returns a stack-friendly [proxykit.Proxy].
	ParseString(input string) (proxykit.Proxy, error)

	// ParseInto resets dst and parses input into it.
	// Successful parsing does not allocate for standard proxy formats.
	// Callers must not access dst during the call. Concurrent calls must use
	// distinct destinations.
	ParseInto(input string, dst *proxykit.Proxy) error

	// ParseBytes parses input into dst without converting input to an
	// allocated string.
	//
	// Parsed string fields alias input. Concurrent calls must use distinct
	// destinations, and callers must keep input immutable while any parsed dst is
	// in use.
	ParseBytes(input []byte, dst *proxykit.Proxy) error
}

type Parse struct {
	format            string
	plan              []parseOp
	strict, hasScheme bool
	kind              parserKind
}

// New compiles format and returns a [*Parse].
//
// Format accepts %t for scheme, %h for host, %d for port, %u for username,
// %p for password, and %% for a literal percent sign.
//
// In strict mode, input must match format exactly. In lenient mode, missing
// credentials are tolerated after a scheme and host are parsed, delimiter
// mismatches and trailing input are ignored, and the final proxy must still
// pass [proxykit.Proxy.IsValid].
func New(format string, strict bool) (*Parse, error) {
	if uint64(len(format)) > uint64(noPlanIndex) {
		return nil, ErrFormatTooLong
	}
	kind := detectParserKind(format, strict)
	if kind != parserGeneric {
		return &Parse{strict: true, hasScheme: true, kind: kind}, nil
	}
	plan := make([]parseOp, 0, countFormatOps(format))
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
			})
			if i-start == 1 {
				plan[len(plan)-1].delimiterByte = format[start]
			}
			continue
		}
		if i+1 >= len(format) {
			return nil, ErrInvalidFormatEndStr
		}
		verb := format[i+1]
		switch verb {
		case 't', 'h', 'd', 'u', 'p':
			plan = append(plan, parseOp{
				credentialEnd: noPlanIndex,
				field:         verb,
			})
			if verb == 't' {
				hasScheme = true
			}
		case '%':
			plan = append(plan, parseOp{
				delimiterStart:  uint32(i),
				delimiterLength: 1,
				delimiterByte:   '%',
			})
		default:
			return nil, ErrInvalidFormatVerb(verb)
		}
		i += 2
	}
	var (
		nextDelimiterByte                       byte
		nextDelimiterStart, nextDelimiterLength uint32
	)
	nextDelimiter, credentialEnd := noPlanIndex, noPlanIndex
	for i := len(plan) - 1; i >= 0; i-- {
		op := &plan[i]
		if op.field == 0 {
			nextDelimiter = uint32(i)
			nextDelimiterStart = op.delimiterStart
			nextDelimiterLength = op.delimiterLength
			nextDelimiterByte = op.delimiterByte
			if op.delimiterLength == 1 && op.delimiterByte == '@' {
				credentialEnd = uint32(i)
			}
			continue
		}
		op.delimiterStart = nextDelimiterStart
		op.delimiterLength = nextDelimiterLength
		op.delimiterByte = nextDelimiterByte
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

// Parse implements [Parse.Parse].
func (pp *Parse) Parse(input string) (*proxykit.Proxy, error) {
	if pp == nil {
		return nil, nil
	}
	switch pp.kind {
	case parserStrictSchemeHostPort:
		before, after, ok := strings.Cut(input, "://")
		if !ok {
			return nil, ErrSubseqDelimNotFound("://")
		}
		scheme := proxykit.ProxyScheme(before)
		endpointValid := proxykit.IsValidScheme(scheme) && proxykit.IsValidHostnamePort(after)
		if !endpointValid && strings.LastIndexByte(after, ':') < 0 {
			return nil, ErrSubseqDelimNotFound(":")
		}
		if !endpointValid {
			return nil, ErrInvalidProxyFormat
		}
		return &proxykit.Proxy{Scheme: scheme, Host: after}, nil
	case parserStrictFullCredentials:
		schemeEnd := strings.Index(input, "://")
		if schemeEnd < 0 {
			return nil, ErrSubseqDelimNotFound("://")
		}
		usernameStart := schemeEnd + 3
		usernameEnd := strings.IndexByte(input[usernameStart:], ':')
		if usernameEnd < 0 {
			return nil, ErrSubseqDelimNotFound(":")
		}
		usernameEnd += usernameStart
		passwordStart := usernameEnd + 1
		passwordEnd := strings.IndexByte(input[passwordStart:], '@')
		if passwordEnd < 0 {
			return nil, ErrSubseqDelimNotFound("@")
		}
		passwordEnd += passwordStart
		host := input[passwordEnd+1:]
		scheme := proxykit.ProxyScheme(input[:schemeEnd])
		endpointValid := proxykit.IsValidScheme(scheme) && proxykit.IsValidHostnamePort(host)
		if !endpointValid && strings.LastIndexByte(host, ':') < 0 {
			return nil, ErrSubseqDelimNotFound(":")
		}
		username, password := input[usernameStart:usernameEnd], input[passwordStart:passwordEnd]
		if !endpointValid || !proxykit.IsValidCredentials(username, password) {
			return nil, ErrInvalidProxyFormat
		}
		return &proxykit.Proxy{Scheme: scheme, Host: host, Username: username, Password: password}, nil
	default:
		var parsed proxykit.Proxy
		if err := pp.ParseInto(input, &parsed); err != nil {
			return nil, err
		}
		return new(parsed), nil
	}
}

// ParseString implements [Parse.ParseString].
func (pp *Parse) ParseString(input string) (proxykit.Proxy, error) {
	var proxy proxykit.Proxy
	err := pp.ParseInto(input, &proxy)
	return proxy, err
}

// ParseBytes implements [Parse.ParseBytes].
func (pp *Parse) ParseBytes(input []byte, proxy *proxykit.Proxy) error {
	return pp.ParseInto(unsafe.String(unsafe.SliceData(input), len(input)), proxy)
}

// ParseInto implements [Parse.ParseInto].
func (pp *Parse) ParseInto(input string, proxy *proxykit.Proxy) error {
	if pp == nil {
		return nil
	}
	if proxy == nil {
		return ErrNilProxy
	}
	proxy.Reset()
	switch pp.kind {
	case parserStrictSchemeHostPort:
		scheme, host, err := parseStrictSchemeHostPort(input)
		proxy.Scheme, proxy.Host = scheme, host
		return err
	case parserStrictFullCredentials:
		scheme, host, username, password, err := parseStrictFullCredentials(input)
		proxy.Scheme, proxy.Host, proxy.Username, proxy.Password = scheme, host, username, password
		return err
	}
	var (
		inputPos     int
		hostStart    int = -1
		hostEnd      int
		hostParsed   bool
		schemeParsed bool
	)
	if !pp.strict && !pp.hasScheme {
		idx := strings.Index(input, "://")
		if idx < 0 {
			return ErrSchemeNotParsed
		}
		proxy.Scheme = proxykit.ProxyScheme(input[:idx])
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
		if !pp.strict && (op.field == 'u' || op.field == 'p') && op.credentialEnd != noPlanIndex {
			if strings.IndexByte(input[inputPos:], '@') < 0 {
				i = int(op.credentialEnd)
				continue
			}
		}
		idx := -1
		if delimiterLength != 0 {
			if delimiterLength == 1 {
				idx = strings.IndexByte(input[start:], op.delimiterByte)
			} else {
				delimiterStart := int(op.delimiterStart)
				nextDelimiter = format[delimiterStart : delimiterStart+delimiterLength]
				idx = strings.Index(input[start:], nextDelimiter)
			}
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
				if at := strings.IndexByte(input[start:], '@'); at >= 0 {
					assignField(proxy, 'u', input[start:start+at])
					inputPos = start + at
				} else if delimiterLength == 1 && op.delimiterByte == ':' {
					assignField(proxy, 'u', input[start:])
					inputPos = len(input)
				}
			case 'p':
				if at := strings.IndexByte(input[start:], '@'); at == 0 {
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
			proxy.Host = capturedValue
			hostStart = start
			hostEnd = inputPos
			hostParsed = capturedValue != ""
		case 'd':
			if proxy.Host == "" {
				proxy.Host = capturedValue
			} else if capturedValue != "" {
				if hostStart >= 0 && hostEnd+1 == start && input[hostEnd] == ':' {
					proxy.Host = input[hostStart:inputPos]
				} else {
					proxy.Host += ":" + capturedValue
				}
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
	} else {
		if !schemeParsed && proxy.Scheme == "" {
			return ErrSchemeNotParsed
		}
		if !hostParsed || proxy.Host == "" {
			return ErrHostNotParsed
		}
	}
	if !proxy.IsValid() {
		return ErrInvalidProxyFormat
	}
	return nil
}

func parseStrictSchemeHostPort(input string) (proxykit.ProxyScheme, string, error) {
	before, after, ok := strings.Cut(input, "://")
	if !ok {
		return "", "", ErrSubseqDelimNotFound("://")
	}
	scheme := proxykit.ProxyScheme(before)
	endpointValid := proxykit.IsValidScheme(scheme) && proxykit.IsValidHostnamePort(after)
	if !endpointValid && strings.LastIndexByte(after, ':') < 0 {
		return "", "", ErrSubseqDelimNotFound(":")
	}
	if !endpointValid {
		return scheme, after, ErrInvalidProxyFormat
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
	endpointValid := proxykit.IsValidScheme(scheme) && proxykit.IsValidHostnamePort(host)
	if !endpointValid && strings.LastIndexByte(host, ':') < 0 {
		return "", "", "", "", ErrSubseqDelimNotFound(":")
	}
	username, password := input[usernameStart:usernameEnd], input[passwordStart:passwordEnd]
	if !endpointValid || !proxykit.IsValidCredentials(username, password) {
		return scheme, host, username, password, ErrInvalidProxyFormat
	}
	return scheme, host, username, password, nil
}

func assignField(proxy *proxykit.Proxy, field byte, val string) {
	switch field {
	case 't':
		proxy.Scheme = proxykit.ProxyScheme(val)
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

func countFormatOps(format string) int {
	var count int
	for i := 0; i < len(format); i++ {
		count++
		if format[i] == '%' {
			i++
			continue
		}
		for i+1 < len(format) && format[i+1] != '%' {
			i++
		}
	}
	return count
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
