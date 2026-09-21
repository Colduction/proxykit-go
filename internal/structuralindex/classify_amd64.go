//go:build !purego && !netbsd

package structuralindex

func init() {
	fastest = detectBackend()
	active = fastest
}

func detectBackend() Backend {
	// The tests mirror internal/cpu: a vector extension counts only when the
	// operating system saves its register state, which XCR0 reports. netbsd
	// is excluded by build constraint because its kernel has corrupted AVX
	// state across signals, and darwin leaves the AVX-512 XCR0 bits clear
	// until first use, so it selects AVX2.
	const (
		osxsave = 1 << 27
		avx     = 1 << 28
		avx2    = 1 << 5
		bmi2    = 1 << 8

		avx512f     = 1 << 16
		avx512bw    = 1 << 30
		avx512vbmi2 = 1 << 6

		ymmState = 0x06
		zmmState = 0xe6
	)
	if maxLeaf, _, _, _ := cpuid(0, 0); maxLeaf < 7 {
		return Portable
	}
	if _, _, ecx, _ := cpuid(1, 0); ecx&(osxsave|avx) != osxsave|avx {
		return Portable
	}
	xcr0, _ := xgetbv()
	_, ebx, ecx, _ := cpuid(7, 0)
	if xcr0&ymmState != ymmState || ebx&avx2 == 0 {
		return Portable
	}
	// The kernel builds its length mask with BZHI, a BMI2 instruction.
	// VBMI2 is not used by the kernel. Requiring it confines 512-bit vectors
	// to processors that run them without lowering the core frequency, the
	// gate simdjson applies to its AVX-512 kernel.
	if xcr0&zmmState == zmmState && ebx&(bmi2|avx512f|avx512bw) == bmi2|avx512f|avx512bw && ecx&avx512vbmi2 != 0 {
		return AVX512
	}
	return AVX2
}

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

func xgetbv() (eax, edx uint32)

//go:noescape
func classify(src *byte, n int, dst *[classCount]uint64)
