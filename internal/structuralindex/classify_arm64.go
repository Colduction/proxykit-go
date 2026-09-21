//go:build !purego

package structuralindex

func init() {
	fastest = NEON
	active = NEON
}

//go:noescape
func classify(src *byte, n int, dst *[classCount]uint64)
