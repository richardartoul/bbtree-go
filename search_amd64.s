//go:build !purego

#include "textflag.h"
#include "go_asm.h"

// Require CPU support (AVX-512F/BW, BMI2, POPCNT) and OS preservation of the opmask and
// ZMM register state. Otherwise the always-available SSE2 path is used.
TEXT ·avx512Supported(SB), NOSPLIT, $0-1
	MOVL $1, AX
	CPUID
	ANDL $0x18800000, CX
	CMPL CX, $0x18800000
	JNE unsupported
	XORL CX, CX
	XGETBV
	ANDL $0xe6, AX
	CMPL AX, $0xe6
	JNE unsupported
	MOVL $7, AX
	XORL CX, CX
	CPUID
	ANDL $0x40010100, BX
	CMPL BX, $0x40010100
	JNE unsupported
	MOVB $1, ret+0(FP)
	RET
unsupported:
	MOVB $0, ret+0(FP)
	RET

// columnBounds counts the first count slots below and equal to target. The
// column is sorted, so the counts bound its equal range. AVX-512 masks the
// tail with BZHI; SSE2 finds the first slot not below (not at most) target
// and clamps it to count. Dispatching here keeps the Go caller inlinable.
TEXT ·columnBounds(SB), NOSPLIT, $0-40
	MOVQ keys+0(FP), AX
	MOVW target+8(FP), CX
	MOVQ count+16(FP), R8
	CMPB ·hasAVX512(SB), $0
	JEQ sse2
	VPBROADCASTW CX, Z0
	VMOVDQU16 (AX), Z1
	VMOVDQU16 64(AX), Z2
	VPCMPUW $1, Z0, Z1, K1
	VPCMPUW $0, Z0, Z1, K2
	VPCMPUW $1, Z0, Z2, K3
	VPCMPUW $0, Z0, Z2, K4
	KUNPCKDQ K1, K3, K1
	KUNPCKDQ K2, K4, K2
	KMOVQ K1, DX
	KMOVQ K2, CX
	BZHIQ R8, DX, DX
	BZHIQ R8, CX, CX
	POPCNTQ DX, DX
	POPCNTQ CX, CX
	ADDQ DX, CX
	MOVQ DX, lo+24(FP)
	MOVQ CX, hi+32(FP)
	VZEROUPPER
	RET

// SSE2 is mandatory on amd64. Flipping the sign bit lets signed PCMPGTW
// compare unsigned windows. Packing two word masks gives one bit per slot.
sse2:
	MOVQ CX, X0
	PSHUFLW $0, X0, X0
	PSHUFD $0, X0, X0
	MOVL $0x80008000, CX
	MOVQ CX, X1
	PSHUFD $0, X1, X1
	PXOR X1, X0
	XORQ R9, R9
	XORQ R10, R10
	XORQ CX, CX

loop:
	MOVOU 0(AX), X2
	MOVOU 16(AX), X3
	PXOR X1, X2
	PXOR X1, X3
	MOVOU X0, X4
	MOVOU X0, X5
	PCMPGTW X2, X4
	PCMPGTW X3, X5
	PCMPEQW X0, X2
	PCMPEQW X0, X3
	PACKSSWB X5, X4
	PACKSSWB X3, X2
	PMOVMSKB X4, DX
	PMOVMSKB X2, BX
	SHLQ CX, DX
	SHLQ CX, BX
	ORQ DX, R9
	ORQ BX, R10
	ADDQ $32, AX
	ADDQ $16, CX
	CMPQ CX, $64
	JNE loop

	ORQ R9, R10
	NOTQ R9
	NOTQ R10
	MOVQ $64, CX
	BSFQ R9, DX
	CMOVQEQ CX, DX
	BSFQ R10, BX
	CMOVQEQ CX, BX
	CMPQ DX, R8
	CMOVQGT R8, DX
	CMPQ BX, R8
	CMOVQGT R8, BX
	MOVQ DX, lo+24(FP)
	MOVQ BX, hi+32(FP)
	RET

// columnBounds32 is columnBounds for four-byte windows, whose high and low
// halves are in separate columns. A slot is below the target if its high
// half is below, or equal with a lower low half.
TEXT ·columnBounds32(SB), NOSPLIT, $0-48
	MOVQ high+0(FP), AX
	MOVQ low+8(FP), DX
	MOVL target+16(FP), CX
	MOVQ count+24(FP), R8
	MOVL CX, BX
	SHRL $16, CX
	CMPB ·hasAVX512(SB), $0
	JEQ sse2
	VPBROADCASTW CX, Z0
	VPBROADCASTW BX, Z5
	VMOVDQU16 (AX), Z1
	VMOVDQU16 (DX), Z2
	VPCMPUW $0, Z0, Z1, K1
	VPCMPUW $1, Z0, Z1, K2
	VPCMPUW $1, Z5, Z2, K1, K3
	VPCMPUW $0, Z5, Z2, K1, K6
	KORD K3, K2, K5
	VMOVDQU16 64(AX), Z1
	VMOVDQU16 64(DX), Z2
	VPCMPUW $0, Z0, Z1, K1
	VPCMPUW $1, Z0, Z1, K2
	VPCMPUW $1, Z5, Z2, K1, K3
	VPCMPUW $0, Z5, Z2, K1, K4
	KORD K3, K2, K2
	KUNPCKDQ K5, K2, K2
	KUNPCKDQ K6, K4, K4
	KMOVQ K2, SI
	KMOVQ K4, CX
	BZHIQ R8, SI, SI
	BZHIQ R8, CX, CX
	POPCNTQ SI, SI
	POPCNTQ CX, CX
	ADDQ SI, CX
	MOVQ SI, lo+32(FP)
	MOVQ CX, hi+40(FP)
	VZEROUPPER
	RET

sse2:
	MOVQ CX, X0
	PSHUFLW $0, X0, X0
	PSHUFD $0, X0, X0
	MOVQ BX, X9
	PSHUFLW $0, X9, X9
	PSHUFD $0, X9, X9
	MOVL $0x80008000, CX
	MOVQ CX, X1
	PSHUFD $0, X1, X1
	PXOR X1, X0
	PXOR X1, X9
	XORQ R9, R9
	XORQ R10, R10
	XORQ CX, CX

loop:
	MOVOU 0(AX), X2
	MOVOU 16(AX), X3
	MOVOU 0(DX), X6
	MOVOU 16(DX), X7
	PXOR X1, X2
	PXOR X1, X3
	PXOR X1, X6
	PXOR X1, X7
	MOVOU X0, X4
	MOVOU X0, X5
	PCMPGTW X2, X4
	PCMPGTW X3, X5
	MOVOU X9, X8
	MOVOU X9, X10
	PCMPGTW X6, X8
	PCMPGTW X7, X10
	PCMPEQW X0, X2
	PCMPEQW X0, X3
	PCMPEQW X9, X6
	PCMPEQW X9, X7
	PAND X2, X8
	PAND X3, X10
	POR X8, X4
	POR X10, X5
	PAND X6, X2
	PAND X7, X3
	PACKSSWB X5, X4
	PACKSSWB X3, X2
	PMOVMSKB X4, SI
	PMOVMSKB X2, DI
	SHLQ CX, SI
	SHLQ CX, DI
	ORQ SI, R9
	ORQ DI, R10
	ADDQ $32, AX
	ADDQ $32, DX
	ADDQ $16, CX
	CMPQ CX, $64
	JNE loop

	ORQ R9, R10
	NOTQ R9
	NOTQ R10
	MOVQ $64, CX
	BSFQ R9, SI
	CMOVQEQ CX, SI
	BSFQ R10, DI
	CMOVQEQ CX, DI
	CMPQ SI, R8
	CMOVQGT R8, SI
	CMPQ DI, R8
	CMOVQGT R8, DI
	MOVQ SI, lo+32(FP)
	MOVQ DI, hi+40(FP)
	RET

// descendBits requires AVX-512 and BMI2. A node's branch pointer is the word
// 16 bytes before its header; children start the branch payload, and each
// child's header is 16 bytes into the child. TestNodeLayout checks this.
// It returns the leaf's header, a pointer into that node's allocation.
// With search set, it also returns the equal range of the
// window at a leaf; otherwise it prefetches the leaf. The leaf pointer
// precedes the header. The function has no safe points, so the GC never
// observes the interior pointers it holds in registers.
// KEYWINDOW broadcasts the window of key bits BX past node AX's prefix into
// Z0, clobbering CX and DI. R11 is zero, and an eight-byte prefix leaves a
// zero window.
#define KEYWINDOW \
	MOVQ header_prefix(AX), CX \
	SHLQ $3, CX \
	SHLXQ CX, BX, DI \
	SHRQ $48, DI \
	CMPQ CX, $64 \
	CMOVQCC R11, DI \
	VPBROADCASTW DI, Z0

// WINDOW leaves masks of node AX's slots below and equal to the window of
// key bits BX in SI and CX, the first masked to its count in R8.
#define WINDOW \
	KEYWINDOW \
	VPCMPUW $6, header_inline(AX), Z0, K1 \
	VPCMPUW $6, (header_inline+64)(AX), Z0, K2 \
	VPCMPUW $0, header_inline(AX), Z0, K3 \
	VPCMPUW $0, (header_inline+64)(AX), Z0, K4 \
	KUNPCKDQ K1, K2, K1 \
	KUNPCKDQ K3, K4, K3 \
	KMOVQ K1, SI \
	KMOVQ K3, CX \
	BZHIQ R8, SI, SI

// Numeric separators are raw eight-byte keys at this branch offset.
// TestNodeLayout checks both signed and unsigned instantiations.
#define BRANCH_KEYS 520

TEXT ·descendBits(SB), NOSPLIT, $0-56
	PCALIGN $64
	MOVQ h+0(FP), AX
	MOVQ bits+8(FP), BX
	MOVQ $0, lo+40(FP)
	MOVQ $0, hi+48(FP)
	XORL R11, R11
	MOVQ -16(AX), DX
	TESTQ DX, DX
	JEQ leaf

internal:
	MOVQ header_count(AX), R8
	MOVQ BX, SI
	XORQ header_low(AX), SI
	TESTQ header_mask(AX), SI
	JNE outside
	WINDOW
	POPCNTQ SI, SI
	BZHIQ R8, CX, CX
	JNE tie

step:
	MOVQ (DX)(SI*8), AX
	ADDQ $16, AX
	MOVQ -16(AX), DX
	TESTQ DX, DX
	JNE internal

leaf:
	CMPB search+24(FP), $0
	JNE search
	// The caller will lock this leaf. A lock serializes later loads, so
	// prefetch the leaf's numeric header and column lines and the payload
	// line holding its lock. Prefetches are hints, not reads of leaf data.
	PREFETCHT0 -16(AX)
	PREFETCHT0 48(AX)
	PREFETCHT0 112(AX)
	MOVQ -8(AX), DX
	PREFETCHT0 (DX)
	JMP done

search:
	MOVQ header_count(AX), R8
	MOVQ BX, SI
	XORQ header_low(AX), SI
	TESTQ header_mask(AX), SI
	JNE leafoutside
	WINDOW
	POPCNTQ SI, SI
	BZHIQ R8, CX, CX
	POPCNTQ CX, CX
	ADDQ SI, CX
	MOVQ SI, lo+40(FP)
	MOVQ CX, hi+48(FP)
	JMP done

	// A key outside the node's prefix sorts wholly before or after it.
outside:
	XORL SI, SI
	CMPQ BX, header_low(AX)
	CMOVQHI R8, SI
	JMP step

leafoutside:
	XORL SI, SI
	CMPQ BX, header_low(AX)
	CMOVQHI R8, SI
	MOVQ SI, lo+40(FP)
	MOVQ SI, hi+48(FP)

done:
	MOVQ AX, node+32(FP)
	VZEROUPPER
	RET

	// Routing keys are immutable even for ConcurrentMap. Equality routes
	// right; only a separator strictly greater than the key stops this walk.
tie:
	POPCNTQ CX, CX
	MOVQ sign+16(FP), R9
	LEAQ BRANCH_KEYS(DX)(SI*8), R10
separator:
	MOVQ (R10), R12
	XORQ R9, R12
	CMPQ R12, BX
	JHI step
	INCQ SI
	ADDQ $8, R10
	DECQ CX
	JNE separator
	JMP step

// descendBatch has no calls or local frame. All 32 typed result slots are
// initialized, including unused lanes. Leaf data requires search=true.
TEXT ·descendBatch(SB), NOSPLIT, $0-608
	MOVQ h+0(FP), AX
	LEAQ nodes+288(FP), R15
	LEAQ bounds+544(FP), DX
	XORL CX, CX
batchinit:
	MOVQ $0, (R15)(CX*8)
	MOVW $-1, (DX)(CX*2)
	INCQ CX
	CMPQ CX, $32
	JB batchinit
	MOVQ count+272(FP), R8
	XORL CX, CX
batchroots:
	CMPQ CX, R8
	JAE batchstart
	MOVQ AX, (R15)(CX*8)
	INCQ CX
	JMP batchroots
batchstart:
	LEAQ bits+8(FP), R11
	MOVQ $-1, R12
	BZHIQ R8, R12, R12
batchpass:
	MOVQ R12, R13
batchlane:
	TESTQ R13, R13
	JEQ batchnext
	BSFQ R13, R14
	BTRQ R14, R13
	MOVQ (R15)(R14*8), AX
	MOVQ -16(AX), DX
	TESTQ DX, DX
	JNE batchroute
	CMPB search+280(FP), $0
	JEQ batchstop
batchroute:
	MOVQ (R11)(R14*8), BX
	MOVQ header_count(AX), R8
	MOVQ header_low(AX), SI
	MOVQ BX, DI
	XORQ SI, DI
	TESTQ header_mask(AX), DI
	JNE batchoutside
	MOVQ header_prefix(AX), CX
	SHLQ $3, CX
	XORL DI, DI
	CMPQ CX, $64
	JAE batchwindow
	MOVQ BX, DI
	SHLQ CX, DI
	SHRQ $48, DI
batchwindow:
	VPBROADCASTW DI, Z0
	VMOVDQU16 header_inline(AX), Z1
	VMOVDQU16 (header_inline+64)(AX), Z2
	VPCMPUW $1, Z0, Z1, K1
	VPCMPUW $0, Z0, Z1, K2
	VPCMPUW $1, Z0, Z2, K3
	VPCMPUW $0, Z0, Z2, K4
	KUNPCKDQ K1, K3, K1
	KUNPCKDQ K2, K4, K2
	KMOVQ K1, SI
	KMOVQ K2, CX
	BZHIQ R8, SI, SI
	BZHIQ R8, CX, CX
	POPCNTQ SI, SI
	TESTQ DX, DX
	JEQ batchfound
	TESTQ CX, CX
	JNE batchtie
	JMP batchstep
batchtie:
	POPCNTQ CX, CX
	MOVQ sign+264(FP), R9
	LEAQ BRANCH_KEYS(DX)(SI*8), R10
batchseparator:
	MOVQ (R10), R8
	XORQ R9, R8
	CMPQ R8, BX
	JHI batchstep
	INCQ SI
	ADDQ $8, R10
	DECQ CX
	JNE batchseparator
	JMP batchstep
batchoutside:
	XORL CX, CX
	XORL SI, SI
	CMPQ BX, header_low(AX)
	JBE batchcheckleaf
	MOVQ R8, SI
batchcheckleaf:
	TESTQ DX, DX
	JEQ batchfound
batchstep:
	MOVQ (DX)(SI*8), AX
	ADDQ $16, AX
	MOVQ AX, (R15)(R14*8)
	PREFETCHT0 -16(AX)
	PREFETCHT0 48(AX)
	PREFETCHT0 112(AX)
	JMP batchlane
batchfound:
	POPCNTQ CX, CX
	ADDQ SI, CX
	SHLQ $8, CX
	ORQ SI, CX
	LEAQ bounds+544(FP), DI
	MOVW CX, (DI)(R14*2)
batchstop:
	BTRQ R14, R12
	JMP batchlane
batchnext:
	TESTQ R12, R12
	JNE batchpass
	VZEROUPPER
	RET

// getBits looks up key bits BX from h for Map.Get and returns the matching
// entry or nil. A leaf's entries start entries bytes past its header, stride
// bytes apart, each with its 8-byte raw key word first. Leaves compare only
// equal windows, testing each candidate's raw key against raw. A window
// collision at an internal node counts the colliding separators at most the
// key, comparing them as ordered bits: raw^bits flips the sign of int64 keys.
// The separators start BRANCH_KEYS bytes into the branch; TestNodeLayout
// checks this. The function has no safe points.
TEXT ·getBits(SB), NOSPLIT, $0-48
	PCALIGN $64
	MOVQ h+0(FP), AX
	MOVQ bits+8(FP), BX
	MOVQ $0, entry+40(FP)
	XORL R11, R11
	MOVQ -16(AX), DX
	TESTQ DX, DX
	JEQ gleaf

ginternal:
	MOVQ header_count(AX), R8
	MOVQ BX, SI
	XORQ header_low(AX), SI
	TESTQ header_mask(AX), SI
	JNE goutside
	WINDOW
	POPCNTQ SI, SI
	BZHIQ R8, CX, CX
	JNE gtie

gstep:
	MOVQ (DX)(SI*8), AX
	ADDQ $16, AX
	MOVQ -16(AX), DX
	TESTQ DX, DX
	JNE ginternal

gleaf:
	MOVQ header_count(AX), R8
	MOVQ BX, SI
	XORQ header_low(AX), SI
	TESTQ header_mask(AX), SI
	JNE gout
	KEYWINDOW
	VPCMPUW $0, header_inline(AX), Z0, K3
	VPCMPUW $0, (header_inline+64)(AX), Z0, K4
	KUNPCKDQ K3, K4, K3
	KMOVQ K3, CX
	BZHIQ R8, CX, CX
	JEQ gout
	MOVQ raw+16(FP), DI
	MOVQ stride+32(FP), R9
	MOVQ entries+24(FP), R10
	ADDQ AX, R10

gcandidate:
	BSFQ CX, SI
	IMULQ R9, SI
	ADDQ R10, SI
	CMPQ DI, (SI)
	JEQ gfound
	LEAQ -1(CX), R12
	ANDQ R12, CX
	JNE gcandidate
	JMP gout

gfound:
	MOVQ SI, entry+40(FP)

gout:
	VZEROUPPER
	RET

	// A key outside the node's prefix sorts wholly before or after it.
goutside:
	XORL SI, SI
	CMPQ BX, header_low(AX)
	CMOVQHI R8, SI
	JMP gstep

	// The colliding separators are slots SI up to SI+popcount(CX). Each one
	// at most the key routes it one child further right.
gtie:
	POPCNTQ CX, CX
	MOVQ raw+16(FP), R9
	XORQ BX, R9
	LEAQ BRANCH_KEYS(DX)(SI*8), R10

gseparator:
	MOVQ (R10), R12
	XORQ R9, R12
	CMPQ R12, BX
	JHI gstep
	INCQ SI
	ADDQ $8, R10
	DECQ CX
	JNE gseparator
	JMP gstep

// prefetchLeaf hints the lines of a leaf's header, its column (the high
// halves, or both halves if wide), and its payload start, which holds the
// lock. The leaf pointer is the word 8 bytes before the header.
TEXT ·prefetchLeaf(SB), NOSPLIT, $0-9
	MOVQ h+0(FP), AX
	PREFETCHT0 -16(AX)
	PREFETCHT0 48(AX)
	PREFETCHT0 112(AX)
	CMPB wide+8(FP), $0
	JEQ payload
	PREFETCHT0 176(AX)
	PREFETCHT0 240(AX)

payload:
	MOVQ -8(AX), DX
	PREFETCHT0 (DX)
	RET

TEXT ·prefetchText(SB), NOSPLIT, $0-24
	MOVQ key_base+0(FP), AX
	MOVQ offset+16(FP), CX
	PREFETCHT0 (AX)(CX*1)
	RET
