//go:build !purego && !netbsd

#include "textflag.h"

DATA newline<>+0(SB)/1, $0x0a
GLOBL newline<>(SB), RODATA|NOPTR, $1

DATA carriageReturn<>+0(SB)/1, $0x0d
GLOBL carriageReturn<>(SB), RODATA|NOPTR, $1

// Register use of both line-end kernels:
//
//	DI  walks dst; R10 is the end of dst.
//	SI  walks src; R9 is the end of src.
//	R11 ends the current run of blocks and R12 is 64 bytes before it.
//	R8  is the end of a line feed at offset 0 of the current block, base+1
//	    at the start.
//	AX, BX hold block masks; CX counts their bits; DX and R13 are scratch.
//
// A block stores at most 64 ends, so a run of room/64 blocks, with room the
// free elements of dst when the run starts, needs no room test per block. A
// run ends at the end of src or when room runs short, and the next run starts
// from the room that remains.

// STEP stores to slot k of DI the end of the lowest line feed in the mask
// register m, which is R8 plus its bit position, and clears that bit. TZCNT of
// zero is 64, so a step past the last line feed stores into a slot beyond the
// count.
#define STEP(k, m) \
	TZCNTQ m, DX; \
	ADDL   R8, DX; \
	MOVL   DX, (k*4)(DI); \
	BLSRQ  m, m

// EXTRACT stores the ends of the mask register m, advances DI past them and R8
// to the next block, and jumps to dense for a block of more than two line
// feeds, which returns to back. Two unconditional stores cover the common block
// of proxy lines: the end of the lowest set bit, found by TZCNT, and the end of
// the highest, found by LZCNT. The two stores do not wait on each other, and
// no dependency runs from block to block through the destination of TZCNT,
// which some Zen 5 processors read (AMD pub. 58455, section 2.9.9). For one
// line feed both stores hold its end, for none they hold ends beyond the count,
// and a block of more than two line feeds rewrites its second slot in DENSE.
#define EXTRACT(m, dense, back) \
	POPCNTQ m, CX; \
	TZCNTQ  m, DX; \
	ADDL    R8, DX; \
	MOVL    DX, (DI); \
	LZCNTQ  m, DX; \
	XORL    $63, DX; \
	ADDL    R8, DX; \
	MOVL    DX, 4(DI); \
	CMPQ    CX, $2; \
	JHI     dense; \
back: \
	LEAQ    (DI)(CX*4), DI; \
	ADDL    $64, R8

// DENSE stores the ends past the first of the mask register m, whose bit count
// is in CX, and jumps back.
#define DENSE(m, dense, more, back) \
dense: \
	BLSRQ  m, m; \
	STEP(1, m); \
	STEP(2, m); \
	STEP(3, m); \
	CMPQ   CX, $4; \
	JLS    back; \
	LEAQ   16(DI), R13; \
more: \
	TZCNTQ m, DX; \
	ADDL   R8, DX; \
	MOVL   DX, (R13); \
	ADDQ   $4, R13; \
	BLSRQ  m, m; \
	JNE    more; \
	JMP    back

// PROLOGUE loads the arguments into the registers above.
#define PROLOGUE \
	MOVQ dst+0(FP), DI; \
	MOVQ room+8(FP), R10; \
	MOVQ src+16(FP), SI; \
	MOVQ n+24(FP), R9; \
	MOVL base+32(FP), R8; \
	ADDQ SI, R9; \
	INCL R8; \
	LEAQ (DI)(R10*4), R10

// ARM sets R11 to the end of the next run and R12 to 64 bytes before it, or
// jumps to done when fewer than 64 elements of dst remain. The room in bytes
// shifted right by 8 is the room in elements divided by 64.
#define ARM \
	MOVQ    R10, R11; \
	SUBQ    DI, R11; \
	SHRQ    $8, R11; \
	JEQ     done; \
	SHLQ    $6, R11; \
	ADDQ    SI, R11; \
	CMPQ    R11, R9; \
	CMOVQHI R9, R11; \
	LEAQ    -64(R11), R12

// RESULTS stores written and consumed.
#define RESULTS \
	SUBQ dst+0(FP), DI; \
	SHRQ $2, DI; \
	MOVQ DI, written+40(FP); \
	SUBQ src+16(FP), SI; \
	MOVQ SI, consumed+48(FP)

// MASK512 sets the mask register mask to the line feeds of the 64 bytes at
// offset off of SI, loaded into vec, and adds its carriage returns to K3.
#define MASK512(off, vec, mask, returns) \
	VMOVDQU64 off(SI), vec; \
	VPCMPEQB  Z17, vec, mask; \
	VPCMPEQB  Z18, vec, returns; \
	KORQ      returns, K3, K3

// func endsAVX512(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)
// Requires n to be a positive multiple of 64, room of at least 64, AVX-512BW,
// BMI1, LZCNT, and POPCNT. The kernel keeps to Z16 and above and to mask registers,
// which leaves the registers that VEX and SSE code shares clean, so it returns
// without VZEROUPPER. K3 collects the carriage returns.
TEXT ·endsAVX512(SB), NOSPLIT, $0-57
	PROLOGUE
	VPBROADCASTB newline<>(SB), Z17
	VPBROADCASTB carriageReturn<>(SB), Z18
	KXORQ        K3, K3, K3

arm:
	ARM
	CMPQ SI, R12
	JAE  single

	PCALIGN $64

pair:
	MASK512(0, Z16, K1, K2)
	MASK512(64, Z19, K4, K5)
	KMOVQ K1, AX
	KMOVQ K4, BX
	EXTRACT(AX, denseA, backA)
	EXTRACT(BX, denseB, backB)
	SUBQ  $-128, SI
	CMPQ  SI, R12
	JB    pair

single:
	CMPQ SI, R11
	JAE  rearm
	MASK512(0, Z16, K1, K2)
	KMOVQ K1, AX
	EXTRACT(AX, denseS, backS)
	ADDQ  $64, SI

rearm:
	CMPQ SI, R9
	JB   arm

done:
	RESULTS
	KORTESTQ K3, K3
	SETNE    AL
	MOVB     AL, cr+56(FP)
	RET

	DENSE(AX, denseA, moreA, backA)
	DENSE(BX, denseB, moreB, backB)
	DENSE(AX, denseS, moreS, backS)

// MASK256 sets r to the line feeds of the 64 bytes at offset off of SI,
// loaded into lo and hi, and adds its carriage returns to Y4. Y5 and Y6 are
// scratch.
#define MASK256(off, lo, hi, r) \
	VMOVDQU   off(SI), lo; \
	VMOVDQU   off+32(SI), hi; \
	VPCMPEQB  lo, Y3, Y5; \
	VPCMPEQB  hi, Y3, Y6; \
	VPOR      Y5, Y6, Y5; \
	VPOR      Y5, Y4, Y4; \
	VPCMPEQB  lo, Y0, lo; \
	VPCMPEQB  hi, Y0, hi; \
	VPMOVMSKB lo, r; \
	VPMOVMSKB hi, DX; \
	SHLQ      $32, DX; \
	ORQ       DX, r

// func endsAVX2(dst *uint32, room int, src *byte, n int, base uint32) (written, consumed int, cr bool)
// Requires n to be a positive multiple of 64, room of at least 64, AVX2, BMI1,
// LZCNT, and POPCNT. Y4 collects the carriage returns.
TEXT ·endsAVX2(SB), NOSPLIT, $0-57
	PROLOGUE
	VPBROADCASTB newline<>(SB), Y0
	VPBROADCASTB carriageReturn<>(SB), Y3
	VPXOR        Y4, Y4, Y4

arm:
	ARM
	CMPQ SI, R12
	JAE  single

	PCALIGN $64

pair:
	MASK256(0, Y1, Y2, AX)
	MASK256(64, Y7, Y8, BX)
	EXTRACT(AX, denseA, backA)
	EXTRACT(BX, denseB, backB)
	SUBQ $-128, SI
	CMPQ SI, R12
	JB   pair

single:
	CMPQ SI, R11
	JAE  rearm
	MASK256(0, Y1, Y2, AX)
	EXTRACT(AX, denseS, backS)
	ADDQ $64, SI

rearm:
	CMPQ SI, R9
	JB   arm

done:
	RESULTS
	VPMOVMSKB Y4, AX
	TESTL     AX, AX
	SETNE     AL
	MOVB      AL, cr+56(FP)
	VZEROUPPER
	RET

	DENSE(AX, denseA, moreA, backA)
	DENSE(BX, denseB, moreB, backB)
	DENSE(AX, denseS, moreS, backS)

// func maxGapAVX2(offsets *uint32, gaps int) uint32
// Requires gaps to be a positive multiple of 8 and offsets to hold gaps+1
// elements, AVX2 in either vector backend. Y0 keeps eight running maxima of
// the differences of neighbouring elements, which unsigned lanes compare.
TEXT ·maxGapAVX2(SB), NOSPLIT, $0-20
	MOVQ  offsets+0(FP), SI
	MOVQ  gaps+8(FP), CX
	VPXOR Y0, Y0, Y0
	PCALIGN $32

loop:
	VMOVDQU 4(SI), Y1
	VPSUBD  (SI), Y1, Y1
	VPMAXUD Y1, Y0, Y0
	ADDQ    $32, SI
	SUBQ    $8, CX
	JNE     loop

	VEXTRACTI128 $1, Y0, X1
	VPMAXUD      X1, X0, X0
	VPSHUFD      $0x4e, X0, X1
	VPMAXUD      X1, X0, X0
	VPSHUFD      $0xb1, X0, X1
	VPMAXUD      X1, X0, X0
	VMOVD        X0, AX
	MOVL         AX, ret+16(FP)
	VZEROUPPER
	RET
