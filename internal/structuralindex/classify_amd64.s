//go:build !purego && !netbsd

#include "go_asm.h"
#include "textflag.h"

// Byte constants that the AVX-512 kernel broadcasts into vectors.
DATA consts<>+0(SB)/1, $0x3a // ':'
DATA consts<>+1(SB)/1, $0x40 // '@'
DATA consts<>+2(SB)/1, $0x2e // '.'
DATA consts<>+3(SB)/1, $0x2d // '-'
DATA consts<>+4(SB)/1, $0x20 // ASCII case bit; also space
DATA consts<>+5(SB)/1, $0x30 // '0'
DATA consts<>+6(SB)/1, $10   // count of digits
DATA consts<>+7(SB)/1, $0x61 // 'a'
DATA consts<>+8(SB)/1, $26   // count of letters
DATA consts<>+9(SB)/1, $0x5f // count of printable bytes, 0x20 to 0x7e
GLOBL consts<>(SB), RODATA|NOPTR, $10

// Offsets into dst, one uint64 per Class.
#define COLON 0
#define AT 8
#define DOT 16
#define HYPHEN 24
#define ALNUM 32
#define UNPRINTABLE 40

// func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL eaxArg+0(FP), AX
	MOVL ecxArg+4(FP), CX
	CPUID
	MOVL AX, eax+8(FP)
	MOVL BX, ebx+12(FP)
	MOVL CX, ecx+16(FP)
	MOVL DX, edx+20(FP)
	RET

// func xgetbv() (eax, edx uint32)
TEXT ·xgetbv(SB), NOSPLIT, $0-8
	XORL CX, CX
	XGETBV
	MOVL AX, eax+0(FP)
	MOVL DX, edx+4(FP)
	RET

// BYTES32 defines a vector of 32 copies of one byte, which q repeats 8 times.
// The AVX2 kernel reads these vectors as memory operands, which costs less
// than broadcasting a byte into a register before each use.
#define BYTES32(name, q) \
	DATA name<>+0(SB)/8, $q; \
	DATA name<>+8(SB)/8, $q; \
	DATA name<>+16(SB)/8, $q; \
	DATA name<>+24(SB)/8, $q; \
	GLOBL name<>(SB), RODATA|NOPTR, $32

BYTES32(ats, 0x4040404040404040)
BYTES32(spaces, 0x2020202020202020)        // ASCII case bit; also space
BYTES32(letterBias, 0x0505050505050505)    // 'a'+5 = 102
BYTES32(letterLimit, 0x6565656565656565)   // 101
BYTES32(printableBias, 0x2121212121212121) // 0x5f+0x21 = 0x80
BYTES32(windowBase, 0x2c2c2c2c2c2c2c2c)    // '-' minus 1
BYTES32(windowEnd, 0x0f0f0f0f0f0f0f0f)

// window maps x-0x2c, clamped to 0..15, to a code in the top three bits.
// Entries 1 to 14 are the bytes '-' to ':', and entries 0 and 15 stand for
// every byte outside that window. Bit 7 marks a digit, bit 6 marks Hyphen and
// Colon, and bit 5 marks Dot and Colon, so one lookup serves four classes and
// Hyphen, Dot and Colon need two bitmaps from the vector unit instead of
// three. VPSHUFB indexes each 128-bit lane on its own, so the kernel
// broadcasts these 16 bytes to both lanes.
DATA window<>+0(SB)/8, $0x8080808000204000
DATA window<>+8(SB)/8, $0x0060808080808080
GLOBL window<>(SB), RODATA|NOPTR, $16

// highIndex holds the positions 32 to 63 of the bytes of the high vector.
DATA highIndex<>+0(SB)/8, $0x2726252423222120
DATA highIndex<>+8(SB)/8, $0x2f2e2d2c2b2a2928
DATA highIndex<>+16(SB)/8, $0x3736353433323130
DATA highIndex<>+24(SB)/8, $0x3f3e3d3c3b3a3938
GLOBL highIndex<>(SB), RODATA|NOPTR, $32

// The fixes align the bitmap in r to the text. A forward load puts byte 0 of
// the text at bit 0, so the fix clears the bits at and above n; the two-vector
// path clears the bytes after the text in the vector instead, so its bitmaps
// need no fix. A backward load ends at the end of the text, so the fix shifts
// out the bits of the bytes before the text and zeros enter at the top.
#define FORWARD32(r) ANDL R8, r
#define BACKWARD32(r) SHRL CX, r
#define FORWARD64(r)
#define BACKWARD64(r) SHRQ CX, r

// STORE32 gathers the byte mask of Y2 and writes the fixed bitmap to dst.
#define STORE32(class, fix) \
	VPMOVMSKB Y2, AX; \
	fix(AX); \
	MOVQ AX, class(DI)

// LETTERS sets m to the byte mask of the letters of x. The signed compare
// tests both ends of the range at once: adding a bias moves the range to the
// top of the positive half, and bytes of 0x80 and above never match.
// A letter is (x|0x20)+5 > 101.
#define LETTERS(x, m) \
	VPOR spaces<>(SB), x, m; \
	VPADDB letterBias<>(SB), m, m; \
	VPCMPGTB letterLimit<>(SB), m, m

// CLASSIFY32 stores the six bitmaps of the 32 bytes in Y0, with the window
// table in Y4. Unprintable needs no compare: x-0x20 is at least 0x5f,
// unsigned, exactly when the saturating sum with 0x21 has its top bit set.
// R10 holds Hyphen and Colon and R11 holds Dot and Colon, so R10&R11 is Colon
// and an exclusive or removes it from both. VPSLLW shifts 16-bit lanes, which
// is harmless because only bit 7 of each byte is gathered and it comes from
// bit 5 of the same byte. Alnum is the digit bit of the code or a letter.
// Each class is gathered and stored as soon as its vector is ready, which
// measures faster than finishing the vector work first.
#define CLASSIFY32(fix) \
	VPCMPEQB ats<>(SB), Y0, Y2; \
	STORE32(AT, fix); \
	VPSUBB spaces<>(SB), Y0, Y2; \
	VPADDUSB printableBias<>(SB), Y2, Y2; \
	STORE32(UNPRINTABLE, fix); \
	VPSUBUSB windowBase<>(SB), Y0, Y2; \
	VPMINUB windowEnd<>(SB), Y2, Y2; \
	VPSHUFB Y2, Y4, Y6; \
	VPADDB Y6, Y6, Y2; \
	VPMOVMSKB Y2, R10; \
	fix(R10); \
	VPSLLW $2, Y6, Y2; \
	VPMOVMSKB Y2, R11; \
	fix(R11); \
	MOVL R10, AX; \
	ANDL R11, AX; \
	XORL AX, R10; \
	XORL AX, R11; \
	MOVQ AX, COLON(DI); \
	MOVQ R10, HYPHEN(DI); \
	MOVQ R11, DOT(DI); \
	LETTERS(Y0, Y2); \
	VPOR Y6, Y2, Y2; \
	STORE32(ALNUM, fix)

// GATHER64 joins the byte masks of lo (low 32 bytes) and hi (high 32 bytes)
// into the fixed bitmap r. It clobbers DX.
#define GATHER64(lo, hi, r, fix) \
	VPMOVMSKB lo, r; \
	VPMOVMSKB hi, DX; \
	SHLQ $32, DX; \
	ORQ DX, r; \
	fix(r)

// CLASSIFY64 stores the six bitmaps of the 64 bytes in Y0 and Y1, with the
// window table in Y4. It applies the tests of CLASSIFY32 to both vectors.
// Y5 is the byte mask of the text in Y1, whose other bytes are already zero.
// A zero byte belongs to Unprintable alone, so Y5 also clears x-0x20 there,
// and the saturating sum of those bytes keeps its top bit clear.
#define CLASSIFY64(fix) \
	VPSUBUSB windowBase<>(SB), Y0, Y2; \
	VPSUBUSB windowBase<>(SB), Y1, Y3; \
	VPMINUB windowEnd<>(SB), Y2, Y2; \
	VPMINUB windowEnd<>(SB), Y3, Y3; \
	VPSHUFB Y2, Y4, Y6; \
	VPSHUFB Y3, Y4, Y7; \
	VPCMPEQB ats<>(SB), Y0, Y2; \
	VPCMPEQB ats<>(SB), Y1, Y3; \
	GATHER64(Y2, Y3, AX, fix); \
	MOVQ AX, AT(DI); \
	VPADDB Y6, Y6, Y8; \
	VPADDB Y7, Y7, Y9; \
	VPSLLW $2, Y6, Y10; \
	VPSLLW $2, Y7, Y11; \
	GATHER64(Y8, Y9, R10, fix); \
	GATHER64(Y10, Y11, R11, fix); \
	MOVQ R10, AX; \
	ANDQ R11, AX; \
	XORQ AX, R10; \
	XORQ AX, R11; \
	MOVQ AX, COLON(DI); \
	MOVQ R10, HYPHEN(DI); \
	MOVQ R11, DOT(DI); \
	VPSUBB spaces<>(SB), Y0, Y2; \
	VPSUBB spaces<>(SB), Y1, Y3; \
	VPAND Y5, Y3, Y3; \
	VPADDUSB printableBias<>(SB), Y2, Y2; \
	VPADDUSB printableBias<>(SB), Y3, Y3; \
	GATHER64(Y2, Y3, AX, fix); \
	MOVQ AX, UNPRINTABLE(DI); \
	LETTERS(Y0, Y2); \
	LETTERS(Y1, Y3); \
	VPOR Y6, Y2, Y2; \
	VPOR Y7, Y3, Y3; \
	GATHER64(Y2, Y3, AX, fix); \
	MOVQ AX, ALNUM(DI)

// EQ512 sets the mask register K2 to the bytes equal to the constant at
// offset c.
#define EQ512(c) \
	VPBROADCASTB consts<>+c(SB), Z17; \
	VPCMPEQB Z17, Z16, K1, K2

// func classify(src *byte, n int, dst *[classCount]uint64)
// Requires 1 <= n <= 64 and an active backend of AVX2 or AVX512. Both kernels
// live in this one function so that the choice costs a compare and a branch.
TEXT ·classify(SB), NOSPLIT, $0-24
	MOVQ src+0(FP), SI
	MOVQ n+8(FP), BX
	MOVQ dst+16(FP), DI
	CMPB ·active(SB), $const_AVX512
	JNE  avx2

	// K1 selects the n valid bytes: BZHI clears the bits at and above n, and
	// none when n is 64. A masked load suppresses faults on the bytes it does
	// not select, so reading at src needs no bounds care, and the masked
	// compares leave every bit at or above n clear. The kernel keeps to Z16
	// and above, which leaves the upper halves of the registers that VEX and
	// SSE code shares clean, so it returns without VZEROUPPER.
	MOVQ  $-1, AX
	BZHIQ BX, AX, AX
	KMOVQ AX, K1
	VMOVDQU8.Z (SI), K1, Z16

	// The parser branches on Colon and At first. A store from a general
	// register forwards to the load that follows sooner than a store from a
	// mask register does, so these two take the extra move.
	EQ512(0)
	KMOVQ K2, AX
	MOVQ  AX, COLON(DI)
	EQ512(1)
	KMOVQ K2, AX
	MOVQ  AX, AT(DI)
	EQ512(2)
	KMOVQ K2, DOT(DI)
	EQ512(3)
	KMOVQ K2, HYPHEN(DI)

	// Alnum is a digit, x-'0' < 10, or a letter, (x|0x20)-'a' < 26, unsigned.
	VPBROADCASTB consts<>+5(SB), Z17
	VPSUBB       Z17, Z16, Z18
	VPBROADCASTB consts<>+6(SB), Z17
	VPCMPUB      $1, Z17, Z18, K1, K3
	VPBROADCASTB consts<>+4(SB), Z19
	VPORQ        Z19, Z16, Z18
	VPBROADCASTB consts<>+7(SB), Z17
	VPSUBB       Z17, Z18, Z18
	VPBROADCASTB consts<>+8(SB), Z17
	VPCMPUB      $1, Z17, Z18, K1, K2
	KORQ         K3, K2, K2
	KMOVQ        K2, ALNUM(DI)

	// Unprintable: x-0x20 >= 0x5f, unsigned. Z19 still holds 0x20.
	VPSUBB       Z19, Z16, Z18
	VPBROADCASTB consts<>+9(SB), Z17
	VPCMPUB      $5, Z17, Z18, K1, K2
	KMOVQ        K2, UNPRINTABLE(DI)
	RET

avx2:
	// A load of whole vectors at src is safe when it stays inside the page of
	// src, which holds valid bytes. Otherwise the load ends at src+n and starts
	// inside that page, so it reads only the text and mapped bytes before it,
	// and each bitmap shifts right by CX so that bit 0 is byte 0 of the text.
	// internal/bytealg applies the same page test to its short inputs.
	// DX is the offset of src in its page.
	VBROADCASTI128 window<>(SB), Y4
	MOVQ SI, DX
	ANDQ $4095, DX
	CMPQ BX, $32
	JHI  long

	// A text of at most 32 bytes takes one vector, which halves the byte mask
	// gathers and needs no joining of halves. CX = 32-n.
	MOVL $32, CX
	SUBL BX, CX
	CMPQ DX, $4064
	JHI  backward32
	MOVL $-1, R8
	SHRL CX, R8 // R8 keeps the low n bits of a bitmap.
	VMOVDQU (SI), Y0
	CLASSIFY32(FORWARD32)
	VZEROUPPER
	RET

backward32:
	VMOVDQU -32(SI)(BX*1), Y0
	CLASSIFY32(BACKWARD32)
	VZEROUPPER
	RET

long:
	CMPQ DX, $4032
	JHI  backward64

	// Y5 selects the bytes of the high vector whose position is below n.
	VMOVD BX, X5
	VPBROADCASTB X5, Y5
	VPCMPGTB highIndex<>(SB), Y5, Y5
	VMOVDQU (SI), Y0
	VPAND 32(SI), Y5, Y1
	CLASSIFY64(FORWARD64)
	VZEROUPPER
	RET

backward64:
	// Both vectors hold text or bytes that the shift by CX = 64-n removes, so
	// Y5 selects every byte.
	MOVL $64, CX
	SUBL BX, CX
	VPCMPEQB Y5, Y5, Y5
	VMOVDQU -64(SI)(BX*1), Y0
	VMOVDQU -32(SI)(BX*1), Y1
	CLASSIFY64(BACKWARD64)
	VZEROUPPER
	RET
