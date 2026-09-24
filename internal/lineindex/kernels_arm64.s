//go:build !purego

#include "textflag.h"

// STEP stores to slot k of R2 the end of the lowest line feed in the mask R5,
// which is R4 plus its bit position, and clears that bit. The count of leading
// zeros of the reversed mask is its bit position, and 64 for zero, so a step
// past the last line feed stores into a slot beyond the count.
#define STEP(k) \
	RBIT R5, R7; \
	CLZ  R7, R7; \
	ADDW R4, R7, R7; \
	MOVW R7, (k*4)(R2); \
	SUB  $1, R5, R8; \
	AND  R8, R5, R5

// func endsNEON(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)
// Requires n to be a positive multiple of 64 and room of at least 64. R2 walks
// dst and R3 is the last position of R2 with room for 64 ends; R0 walks src
// and R1 is its end; R4 is the end of a line feed at offset 0 of the block.
// NEON has no move-mask instruction: each compare byte keeps the weight of its
// position within its group of eight, and three pairwise additions sum every
// group into one byte of the 64-bit mask. V19 collects the carriage returns.
TEXT ·endsNEON(SB), NOSPLIT, $0-57
	MOVD  dst+0(FP), R2
	MOVD  room+8(FP), R3
	MOVD  src+16(FP), R0
	MOVD  n+24(FP), R1
	MOVWU base+32(FP), R4
	ADD   R0, R1, R1
	ADDW  $1, R4, R4
	SUB   $64, R3, R3
	ADD   R3<<2, R2, R3
	VMOVI $0x0a, V16.B16
	VMOVI $0x0d, V18.B16
	VMOVQ $0x8040201008040201, $0x8040201008040201, V17
	VEOR  V19.B16, V19.B16, V19.B16

loop:
	VLD1.P  64(R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VCMEQ   V18.B16, V0.B16, V8.B16
	VCMEQ   V18.B16, V1.B16, V9.B16
	VCMEQ   V18.B16, V2.B16, V10.B16
	VCMEQ   V18.B16, V3.B16, V11.B16
	VORR    V9.B16, V8.B16, V8.B16
	VORR    V11.B16, V10.B16, V10.B16
	VORR    V10.B16, V8.B16, V8.B16
	VORR    V8.B16, V19.B16, V19.B16
	VCMEQ   V16.B16, V0.B16, V4.B16
	VCMEQ   V16.B16, V1.B16, V5.B16
	VCMEQ   V16.B16, V2.B16, V6.B16
	VCMEQ   V16.B16, V3.B16, V7.B16
	VAND    V17.B16, V4.B16, V4.B16
	VAND    V17.B16, V5.B16, V5.B16
	VAND    V17.B16, V6.B16, V6.B16
	VAND    V17.B16, V7.B16, V7.B16
	VADDP   V5.B16, V4.B16, V4.B16
	VADDP   V7.B16, V6.B16, V6.B16
	VADDP   V6.B16, V4.B16, V4.B16
	VADDP   V4.B16, V4.B16, V4.B16
	VMOV    V4.D[0], R5
	VCNT    V4.B8, V5.B8
	VUADDLV V5.B8, V5
	VMOV    V5.H[0], R6
	STEP(0)
	STEP(1)
	CMP     $2, R6
	BHI     dense

advance:
	ADD  R6<<2, R2, R2
	ADDW $64, R4, R4
	CMP  R1, R0
	BHS  done
	CMP  R3, R2
	BLS  loop

done:
	MOVD dst+0(FP), R7
	SUB  R7, R2, R2
	LSR  $2, R2, R2
	MOVD R2, written+40(FP)
	MOVD src+16(FP), R7
	SUB  R7, R0, R0
	MOVD R0, consumed+48(FP)
	VMOV V19.D[0], R7
	VMOV V19.D[1], R8
	ORR  R8, R7, R7
	CMP  $0, R7
	CSET NE, R7
	MOVB R7, cr+56(FP)
	RET

dense:
	STEP(2)
	STEP(3)
	CMP $4, R6
	BLS advance
	ADD $16, R2, R9

more:
	RBIT   R5, R7
	CLZ    R7, R7
	ADDW   R4, R7, R7
	MOVW.P R7, 4(R9)
	SUB    $1, R5, R8
	AND    R8, R5, R5
	CBNZ   R5, more
	B      advance

// func maxGapNEON(offsets *uint32, gaps int) uint32
// Requires gaps to be a positive multiple of 4 and offsets to hold gaps+1
// elements. V2 keeps four running maxima of the differences of neighbouring
// elements, which R0 and R3, one element apart, load.
TEXT ·maxGapNEON(SB), NOSPLIT, $0-20
	MOVD offsets+0(FP), R0
	MOVD gaps+8(FP), R1
	ADD  $4, R0, R3
	VEOR V2.B16, V2.B16, V2.B16

loop:
	VLD1.P 16(R0), [V0.S4]
	VLD1.P 16(R3), [V1.S4]
	VSUB   V0.S4, V1.S4, V1.S4
	VUMAX  V1.S4, V2.S4, V2.S4
	SUBS   $4, R1, R1
	BNE    loop

	VUMAXV V2.S4, V2
	VMOV   V2.S[0], R0
	MOVW   R0, ret+16(FP)
	RET
