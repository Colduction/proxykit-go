package structuralindex

// A Backend identifies the implementation that fills an [Index].
type Backend uint8

// The backends. The amd64 assembly reads the value of AVX512.
const (
	// Portable uses no vector kernel: [Index.Build] reports false
	// and callers rely on [IsHostName] and [IsPrintable].
	Portable Backend = iota
	// AVX2 is the amd64 kernel for 256-bit vectors. It also requires POPCNT,
	// BMI1, and LZCNT.
	AVX2
	// AVX512 is the amd64 kernel for 512-bit vectors with byte masks.
	AVX512
	// NEON is the arm64 kernel.
	NEON
)

var active, fastest Backend

// String returns the name of b.
func (b Backend) String() string {
	switch b {
	case Portable:
		return "portable"
	case AVX2:
		return "avx2"
	case AVX512:
		return "avx512"
	case NEON:
		return "neon"
	default:
		return "unknown"
	}
}

// ActiveBackend returns the backend that [Index.Build] uses.
// At program start it is the fastest one the processor
// and operating system support.
func ActiveBackend() Backend {
	return active
}

// SetBackend makes b the active backend and returns the previous one.
// It ignores a backend the processor does not support,
// so the result of a following [ActiveBackend] call tells whether b took effect.
// It exists for tests and benchmarks that compare backends
// and must not run concurrently with [Index.Build].
func SetBackend(b Backend) Backend {
	previous := active
	if b == Portable || b == fastest || (b == AVX2 && fastest == AVX512) {
		active = b
	}
	return previous
}
