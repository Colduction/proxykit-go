//go:build !purego

#include "textflag.h"

// The kernel classifies one 16-byte block at a time, so its work follows the
// length of the text. The constants stay in V21 to V29, the blocks in V0 to
// V3, and the bitmaps of the blocks, six 16-bit lanes each, in V16 to V19.

// BLOCK sets the first six 16-bit lanes of out to the bitmaps of the block in,
// in the order of the classes. With y = x-0x20, ':' is y == 26, '@' is
// y == 0x20, '.' is y == 14, '-' is y == 13, and Unprintable is y > 0x5e.
// Alnum is a letter, (x|0x20)-'a' < 26, or a digit, x-'0' < 10.
// NEON has no move-mask instruction: each mask byte keeps the bit weight of
// its position within its group of eight, and three pairwise additions sum
// every group into one byte.
#define BLOCK(in, out) \
	VSUB  V25.B16, in.B16, V9.B16; \
	VCMEQ V21.B16, V9.B16, V4.B16; \
	VCMEQ V25.B16, V9.B16, V5.B16; \
	VCMEQ V22.B16, V9.B16, V6.B16; \
	VCMEQ V23.B16, V9.B16, V7.B16; \
	VORR  V25.B16, in.B16, V8.B16; \
	VSUB  V24.B16, V8.B16, V8.B16; \
	VCMHI V8.B16, V21.B16, V8.B16; \
	VSUB  V26.B16, in.B16, V11.B16; \
	VCMHI V11.B16, V27.B16, V11.B16; \
	VORR  V11.B16, V8.B16, V8.B16; \
	VCMHI V28.B16, V9.B16, V9.B16; \
	VAND  V29.B16, V4.B16, V4.B16; \
	VAND  V29.B16, V5.B16, V5.B16; \
	VAND  V29.B16, V6.B16, V6.B16; \
	VAND  V29.B16, V7.B16, V7.B16; \
	VAND  V29.B16, V8.B16, V8.B16; \
	VAND  V29.B16, V9.B16, V9.B16; \
	VADDP V5.B16, V4.B16, V4.B16; \
	VADDP V7.B16, V6.B16, V6.B16; \
	VADDP V9.B16, V8.B16, V8.B16; \
	VADDP V6.B16, V4.B16, V4.B16; \
	VADDP V8.B16, V8.B16, V8.B16; \
	VADDP V8.B16, V4.B16, out.B16

// func classify(src *byte, n int, dst *[classCount]uint64)
TEXT ·classify(SB), NOSPLIT, $0-24
	MOVD  src+0(FP), R0
	MOVD  n+8(FP), R1
	MOVD  dst+16(FP), R2
	VMOVI $26, V21.B16
	VMOVI $14, V22.B16
	VMOVI $13, V23.B16
	VMOVI $0x61, V24.B16
	VMOVI $0x20, V25.B16
	VMOVI $0x30, V26.B16
	VMOVI $10, V27.B16
	VMOVI $0x5e, V28.B16
	VMOVQ $0x8040201008040201, $0x8040201008040201, V29

	// arm64 memory tagging forbids reading past the text, so the last block
	// is the 16 bytes that end with the text. Bits 4 and 5 of R3 number that
	// block, and V20 is the negative count of the bytes before it that the
	// load repeats. Shifting the bitmaps of the block right by that count
	// drops the repeated bytes and clears the bits past the text.
	SUB  $1, R1, R3
	ORR  $-16, R3, R4
	ADD  $1, R4, R4
	VDUP R4, V20.H8
	SUBS $16, R1, R5
	BLO  short
	ADD  R0, R5, R5
	VLD1 (R5), [V3.B16]

last:
	BLOCK(V3, V10)
	TBNZ $5, R3, long
	TBZ  $4, R3, one
	VLD1  (R0), [V0.B16]
	VUSHL V20.H8, V10.H8, V17.H8
	B     first

long:
	TBZ   $4, R3, three
	VLD1  (R0), [V0.B16, V1.B16, V2.B16]
	VUSHL V20.H8, V10.H8, V19.H8
	BLOCK(V2, V18)
	B     second

three:
	VLD1  (R0), [V0.B16, V1.B16]
	VUSHL V20.H8, V10.H8, V18.H8
	VEOR  V19.B16, V19.B16, V19.B16

second:
	BLOCK(V1, V17)

first:
	BLOCK(V0, V16)
	TBNZ $5, R3, interleave

	// Two blocks make 32-bit lanes, which widen like those of one block.
	VZIP1 V17.H8, V16.H8, V12.H8
	VZIP2 V17.H8, V16.H8, V13.H8
	B     widen

one:
	VUSHL  V20.H8, V10.H8, V16.H8
	VUXTL  V16.H4, V12.S4
	VUXTL2 V16.H8, V13.S4

widen:
	VUXTL  V12.S2, V4.D2
	VUXTL2 V12.S4, V5.D2
	VUXTL  V13.S2, V6.D2
	VST1   [V4.B16, V5.B16, V6.B16], (R2)
	RET

interleave:
	// Lane k of every block joins into the 64-bit bitmap of class k.
	VZIP1 V17.H8, V16.H8, V12.H8
	VZIP2 V17.H8, V16.H8, V13.H8
	VZIP1 V19.H8, V18.H8, V14.H8
	VZIP2 V19.H8, V18.H8, V15.H8
	VZIP1 V14.S4, V12.S4, V4.S4
	VZIP2 V14.S4, V12.S4, V5.S4
	VZIP1 V15.S4, V13.S4, V6.S4
	VST1  [V4.B16, V5.B16, V6.B16], (R2)
	RET

short:
	// Text under 16 bytes is gathered with loads that stay inside it,
	// so that it ends the block like longer text does.
	TBZ   $3, R1, tiny
	MOVD  (R0), R5
	NEG   R1<<3, R6
	LSL   R6, R5, R5
	FMOVD R5, F3
	ADD   R0, R1, R5
	MOVD  -8(R5), R5
	VMOV  R5, V3.D[1]
	B     last

tiny:
	MOVBU.P 1(R0), R6
	LSR     $8, R5, R5
	ORR     R6<<56, R5, R5
	SUBS    $1, R1, R1
	BNE     tiny
	VMOV    R5, V3.D[1]
	B       last
