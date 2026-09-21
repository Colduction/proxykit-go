package proxyparser

import (
	"math/bits"
	"strings"
	"unsafe"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/internal/structuralindex"
)

const (
	schemeSeparator = "://"

	wordHTTP  = 'h' | 't'<<8 | 't'<<16 | 'p'<<24 | ':'<<32 | '/'<<40 | '/'<<48
	wordHTTPS = 'h' | 't'<<8 | 't'<<16 | 'p'<<24 | 's'<<32 | ':'<<40 | '/'<<48 | '/'<<56
	wordSOCKS = 's' | 'o'<<8 | 'c'<<16 | 'k'<<24 | 's'<<32

	wordSOCKS4  = wordSOCKS | '4'<<40 | ':'<<48 | '/'<<56
	wordSOCKS5  = wordSOCKS | '5'<<40 | ':'<<48 | '/'<<56
	wordSOCKS4A = wordSOCKS | '4'<<40 | 'a'<<48 | ':'<<56
	wordSOCKS5H = wordSOCKS | '5'<<40 | 'h'<<48 | ':'<<56

	eachByte = structuralindex.EachByte
	highBits = structuralindex.HighBits
	lowSeven = structuralindex.LowSeven

	wordShortestIPv4  = '0' | '.'<<8 | '0'<<16 | '.'<<24 | '0'<<32 | '.'<<40 | '0'<<48
	limitShortestIPv4 = 0x76 | 0x7f<<8 | 0x76<<16 | 0x7f<<24 | 0x76<<32 | 0x7f<<40 | 0x76<<48

	wordMaxPort = '6'<<32 | '5'<<24 | '5'<<16 | '3'<<8 | '5'

	maxPortDigits       = 5
	maxCredentialLength = 255
)

var (
	leastOctet = [8]uint64{0: 1, 1: '0', 2: '1'<<8 | '0', 3: '1'<<16 | '0'<<8 | '0', 4: 1, 5: 1, 6: 1, 7: 1}
	octetSpan  = [8]uint64{1: 9, 2: '9'<<8 | '9' - ('1'<<8 | '0'), 3: '2'<<16 | '5'<<8 | '5' - ('1'<<16 | '0'<<8 | '0')}
)

func parseStrictFast(input string, proxy *proxykit.Proxy, credentials bool) (handled bool, err error) {
	// The fast path accepts a subset of what proxykit.Proxy.Validate accepts:
	// a canonical lowercase scheme, a relative DNS name or dotted IPv4 host,
	// a port of at most five digits, and printable credentials. Input outside
	// that subset, such as an IPv6 literal, an uppercase scheme, a root-dot
	// name, or anything invalid, is left to the scalar parser, which owns the
	// validation errors. The one error reported here is a credential
	// delimiter that is missing after a canonical scheme, which the scalar
	// parser reports before it validates anything.
	if len(input) < len("http://a:1") {
		return false, nil
	}
	// The scheme and its "://" are compared as words. It is done here, not
	// in a helper, because the helper exceeds the inlining budget and a call
	// costs more than the comparison.
	var schemeLen int
	switch w := structuralindex.Load64(input, 0); {
	case w&(1<<56-1) == wordHTTP:
		schemeLen = len(proxykit.HTTP)
	case w == wordHTTPS:
		schemeLen = len(proxykit.HTTPS)
	case (w == wordSOCKS4 || w == wordSOCKS5) && input[8] == '/':
		schemeLen = len(proxykit.SOCKS5)
	case (w == wordSOCKS4A || w == wordSOCKS5H) && input[8] == '/' && input[9] == '/':
		schemeLen = len(proxykit.SOCKS5H)
	default:
		return false, nil
	}
	scheme := proxykit.ProxyScheme(input[:schemeLen])
	hostStart := schemeLen + len(schemeSeparator)
	if !credentials {
		// Without an index isHostPort reads no Index, so none is built.
		if !isHostPort(nil, false, input, hostStart) {
			return false, nil
		}
		*proxy = proxykit.Proxy{Scheme: scheme, Host: input[hostStart:]}
		return true, nil
	}
	// The scalar parser ends the username at the first colon and the
	// password at the first at sign after it, so the fast path does too.
	// Both searches also test that every byte up to the at sign is printable
	// ASCII. A colon is printable, so one test covers both values, and the
	// username holds no colon, which is the extra rule for HTTP schemes.
	usernameStart := hostStart
	var (
		ix                       structuralindex.Index
		usernameEnd, passwordEnd int
		printable                bool
	)
	indexed := ix.Build(input)
	if indexed {
		usernameEnd, passwordEnd = ix.Next(structuralindex.Colon, usernameStart), -1
		if usernameEnd >= 0 {
			passwordEnd = ix.Next(structuralindex.At, usernameEnd+1)
		}
		printable = passwordEnd >= 0 &&
			ix.Bitmap(structuralindex.Unprintable)&structuralindex.Range(usernameStart, passwordEnd) == 0
	} else {
		usernameEnd, passwordEnd, printable = scanCredentials(input, usernameStart)
	}
	if passwordEnd < 0 {
		*proxy = proxykit.Proxy{}
		if usernameEnd < 0 {
			return true, ErrSubseqDelimNotFound(":")
		}
		return true, ErrSubseqDelimNotFound("@")
	}
	username, password := input[usernameStart:usernameEnd], input[usernameEnd+1:passwordEnd]
	if !printable || !isCredentialPair(scheme, username, password) || !isHostPort(&ix, indexed, input, passwordEnd+1) {
		return false, nil
	}
	*proxy = proxykit.Proxy{Scheme: scheme, Host: input[passwordEnd+1:], Username: username, Password: password}
	return true, nil
}

func isValidFast(ix *structuralindex.Index, indexed bool, input string, proxy *proxykit.Proxy) bool {
	scheme, host := proxy.Scheme, proxy.Host
	if !scheme.IsValid() {
		return false
	}
	if username, password := proxy.Username, proxy.Password; username != "" || password != "" {
		if !isCredentialPair(scheme, username, password) ||
			(scheme == proxykit.HTTP || scheme == proxykit.HTTPS) && strings.IndexByte(username, ':') >= 0 ||
			!structuralindex.IsPrintable(username) || !structuralindex.IsPrintable(password) {
			return false
		}
	}
	// The index describes input, so it serves a host that aliases input. The
	// addresses are only compared, never turned back into pointers. A host
	// joined from separate captures lies elsewhere and takes the word tests.
	if indexed && host != "" && len(host) <= len(input) {
		offset := uintptr(unsafe.Pointer(unsafe.StringData(host))) - uintptr(unsafe.Pointer(unsafe.StringData(input)))
		if offset <= uintptr(len(input)-len(host)) {
			return isHostPort(ix, true, input[:int(offset)+len(host)], int(offset))
		}
	}
	return isHostPort(ix, false, host, 0)
}

func indexSchemeSeparator(ix *structuralindex.Index, indexed bool, input string, from int) int {
	// The result is relative to from, like strings.Index(input[from:], "://").
	if !indexed {
		return strings.Index(input[from:], schemeSeparator)
	}
	// A separator begins at a colon, so the two bytes after each colon decide.
	for colons := ix.Bitmap(structuralindex.Colon) &^ structuralindex.LowMask(from); colons != 0; colons &= colons - 1 {
		colon := bits.TrailingZeros64(colons)
		if colon+2 < len(input) && input[colon+1] == '/' && input[colon+2] == '/' {
			return colon - from
		}
	}
	return -1
}

func indexByteFrom(ix *structuralindex.Index, indexed bool, input string, from int, b byte, class structuralindex.Class) int {
	// The result is relative to from, like strings.IndexByte(input[from:], b).
	if !indexed || class == noClass {
		return strings.IndexByte(input[from:], b)
	}
	if position := ix.Next(class, from); position >= 0 {
		return position - from
	}
	return -1
}

func isCredentialPair(scheme proxykit.ProxyScheme, username, password string) bool {
	// These are the rules of proxykit.IsValidCredentialsFor that do not
	// depend on the bytes of the values.
	if len(username) > maxCredentialLength || len(password) > maxCredentialLength {
		return false
	}
	if password == "" {
		return true
	}
	return username != "" && scheme != proxykit.SOCKS4 && scheme != proxykit.SOCKS4A
}

func scanCredentials(input string, start int) (usernameEnd, passwordEnd int, printable bool) {
	// The results are the positions of the first colon at or after start and
	// of the first at sign after it, each -1 when missing, and whether every
	// byte from start to the at sign is printable ASCII. input holds at least
	// eight bytes.
	n := len(input)
	usernameEnd = -1
	target := uint64(':' * eachByte)
	var unprintable uint64
	for i := start; i < n; {
		// The final word ends at the last byte, so it may begin before i.
		// The bytes before i take part in the printable test a second time.
		at := min(i, n-8)
		w := structuralindex.Load64(input, at)
		// The word may reach past the at sign into the host, where an
		// unprintable byte is invalid as well.
		unprintable |= structuralindex.UnprintableBytes(w)
		// A byte of found is zero when it is the target, and the bytes before
		// i are made nonzero. The subtraction then sets bit 7 of the lowest
		// zero byte and of no byte below it, so the lowest set bit is exact.
		found := (w ^ target) | (1<<(uint(i-at)*8) - 1)
		found = (found - eachByte) &^ found & highBits
		if found == 0 {
			i = at + 8
			continue
		}
		position := at + bits.TrailingZeros64(found)>>3
		if usernameEnd >= 0 {
			return usernameEnd, position, unprintable&highBits == 0
		}
		usernameEnd, target, i = position, '@'*eachByte, position+1
	}
	return usernameEnd, -1, false
}

func isHostPort(ix *structuralindex.Index, indexed bool, input string, start int) bool {
	// A true result means proxykit.IsValidHostnamePort accepts input[start:].
	// A bracketed IP literal belongs to the scalar parser.
	if uint(start) >= uint(len(input)) || input[start] == '[' {
		return false
	}
	// The port is at most five digits with a value of 1 to 65535, preceded by
	// a colon that is not the first byte. The scan for its start is a loop
	// because the branch predictor then supplies the host length early.
	n := len(input)
	portStart := n
	for portStart > start && input[portStart-1]-'0' <= 9 {
		portStart--
	}
	digits, hostEnd := uint(n-portStart), portStart-1
	if digits-1 >= maxPortDigits || hostEnd <= start || input[hostEnd] != ':' {
		return false
	}
	// The word holds the last eight bytes with the final byte lowest. As a
	// big-endian number the digits order like the value they spell, and fewer
	// than five digits always spell less than wordMaxPort, which is "65535".
	// The low nibbles are all zero only for a port of zero.
	var tail uint64
	if n >= 8 {
		tail = bits.ReverseBytes64(structuralindex.Load64(input, n-8))
	} else {
		for i := range n {
			tail = tail<<8 | uint64(input[i])
		}
	}
	if port := tail & (1<<(digits*8) - 1); port > wordMaxPort || port&(0x0f*eachByte) == 0 {
		return false
	}
	// A final label of digits must belong to an IPv4 address. Deciding by the
	// last byte alone sends the rare name such as "proxy1" to the scalar
	// parser, which is correct though slower.
	if last := input[hostEnd-1]; last-'0' <= 9 {
		return isIPv4(input[start:hostEnd])
	}
	if !indexed {
		return structuralindex.IsHostName(input[start:hostEnd])
	}
	// Every byte must be a name byte, and no dot or hyphen may sit on a label
	// edge: beside a dot or at either end of the host. The index covers at
	// most 64 bytes, so no label can exceed the 63-byte DNS limit.
	host := structuralindex.Range(start, hostEnd)
	dots := ix.Bitmap(structuralindex.Dot) & host
	edges := dots | ix.Bitmap(structuralindex.Hyphen)&host
	names := edges | ix.Bitmap(structuralindex.Alnum)&host
	return names == host && edges&(dots<<1|dots>>1|1<<uint(start)|1<<uint(hostEnd-1)) == 0
}

func isIPv4(host string) bool {
	// The form is the one proxykit.IsValidHost requires of a name whose final
	// label is numeric: four decimal octets of one to three digits, without
	// leading zeros, each at most 255.
	n := len(host)
	if n < len("0.0.0.0") || n > len("255.255.255.255") {
		return false
	}
	if n == len("0.0.0.0") {
		// Only one digit per octet fits. The XOR with "0.0.0.0" leaves zero
		// for each dot and the digit value for each digit. The limit then
		// sets bit 7 of a byte that exceeds 9 under a digit or 0 under a dot.
		difference := (structuralindex.Load32(host, 0) | structuralindex.Load32(host, 3)<<24) ^ wordShortestIPv4
		return (difference+limitShortestIPv4|difference)&highBits == 0
	}
	// The first word holds bytes 0 to 7 with byte 0 lowest, and the last word
	// holds bytes n-8 to n-1 with byte n-1 lowest. Octets of valid length put
	// the first two dots in the first word and the last two in the last word.
	first, last := structuralindex.Load64(host, 0), bits.ReverseBytes64(structuralindex.Load64(host, n-8))
	firstOther, lastOther := first^'.'*eachByte, last^'.'*eachByte
	firstOther |= firstOther&lowSeven + lowSeven
	lastOther |= lastOther&lowSeven + lowSeven
	// Bit 7 of a byte is now set unless the byte is a dot. A digit neither
	// carries nor borrows in the range test, so the lowest byte of a word
	// that is neither digit nor dot always fails it.
	firstDigits := (first + (0x80-'0')*eachByte) & ((0x80+'9')*eachByte - first)
	lastDigits := (last + (0x80-'0')*eachByte) & ((0x80+'9')*eachByte - last)
	if (firstOther&^firstDigits|lastOther&^lastDigits)&highBits != 0 {
		return false
	}
	// The lengths of the first two octets follow from the two lowest dots of
	// the first word, and those of the last two octets from the two lowest
	// dots of the last word. A missing dot counts as position 8, which makes
	// a length that the octet test rejects.
	firstDots, lastDots := ^firstOther&highBits, ^lastOther&highBits
	firstLen := uint(bits.TrailingZeros64(firstDots)) >> 3
	firstDots &= firstDots - 1
	secondDot := uint(bits.TrailingZeros64(firstDots)) >> 3
	fourthLen := uint(bits.TrailingZeros64(lastDots)) >> 3
	lastDots &= lastDots - 1
	secondDotFromEnd := uint(bits.TrailingZeros64(lastDots)) >> 3
	// The name has exactly three dots when the second dot from the start is
	// also the second dot from the end.
	if secondDot+secondDotFromEnd+1 != uint(n) {
		return false
	}
	secondLen, thirdLen := secondDot-firstLen-1, secondDotFromEnd-fourthLen-1
	// With the first byte of an octet on top, the octet shifted down to the
	// low bytes reads as a big-endian number whose order is that of its
	// digits.
	first = bits.ReverseBytes64(first)
	return isOctet(first>>((64-firstLen*8)&63), firstLen) &&
		isOctet(first<<((firstLen+1)*8&63)>>((64-secondLen*8)&63), secondLen) &&
		isOctet(last>>((fourthLen+1)*8&63)&(1<<(thirdLen*8&63)-1), thirdLen) &&
		isOctet(last&(1<<(fourthLen*8&63)-1), fourthLen)
}

func isOctet(digits uint64, n uint) bool {
	// digits holds n ASCII digits as a big-endian number. The tables give the
	// least such number without a leading zero and the distance from it to
	// the greatest one, "255" for three digits. No number of digits passes
	// for an n other than 1, 2, or 3.
	return digits-leastOctet[n&7] <= octetSpan[n&7]
}
