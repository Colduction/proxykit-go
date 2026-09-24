//go:build !purego && !netbsd

package structuralindex

func init() {
	fastest = detectBackend()
	active = fastest
}

func detectBackend() Backend {
	const (
		popcnt  = 1 << 23
		osxsave = 1 << 27
		avx     = 1 << 28
		bmi1    = 1 << 3
		avx2    = 1 << 5
		bmi2    = 1 << 8

		avx512f     = 1 << 16
		avx512bw    = 1 << 30
		avx512vbmi2 = 1 << 6

		lzcnt = 1 << 5

		ymmState = 0x06
		zmmState = 0xe6
	)
	if maxLeaf, _, _, _ := cpuid(0, 0); maxLeaf < 7 {
		return Portable
	}
	if _, _, ecx, _ := cpuid(1, 0); ecx&(popcnt|osxsave|avx) != popcnt|osxsave|avx {
		return Portable
	}
	if maxExtended, _, _, _ := cpuid(0x80000000, 0); maxExtended < 0x80000001 {
		return Portable
	}
	if _, _, ecx, _ := cpuid(0x80000001, 0); ecx&lzcnt == 0 {
		return Portable
	}
	xcr0, _ := xgetbv()
	_, ebx, ecx, _ := cpuid(7, 0)
	if xcr0&ymmState != ymmState || ebx&(avx2|bmi1) != avx2|bmi1 {
		return Portable
	}
	if xcr0&zmmState == zmmState && ebx&(bmi2|avx512f|avx512bw) == bmi2|avx512f|avx512bw && ecx&avx512vbmi2 != 0 {
		return AVX512
	}
	return AVX2
}

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

func xgetbv() (eax, edx uint32)

//go:noescape
func classify(src *byte, n int, dst *[classCount]uint64)
