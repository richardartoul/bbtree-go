//go:build !purego

#include "textflag.h"
#include "go_asm.h"

// Go's assembler lacks the unsigned vector compares. CMHI sets the lanes of
// Vd where Vn is higher than Vm, and CMHS where it is higher or the same.
#define VCMHI_H8(m, n, d) WORD $(0x6E603400 | (m)<<16 | (n)<<5 | (d))
#define VCMHS_H8(m, n, d) WORD $(0x6E603C00 | (m)<<16 | (n)<<5 | (d))

// Columns are sorted, and slots past a node's count hold all-ones windows,
// which never sort below a window. So the number of slots below a window is
// its lower bound, and the number at most it, clamped to the count, is its
// upper bound. Counting whole columns avoids masking the tail.

// BOUNDS leaves masks of the slots in V1-V8 below the window in V0 in V9-V16
// and of those at most it in V17-V24.
#define BOUNDS \
	VCMHI_H8(1, 0, 9) \
	VCMHI_H8(2, 0, 10) \
	VCMHI_H8(3, 0, 11) \
	VCMHI_H8(4, 0, 12) \
	VCMHI_H8(5, 0, 13) \
	VCMHI_H8(6, 0, 14) \
	VCMHI_H8(7, 0, 15) \
	VCMHI_H8(8, 0, 16) \
	VCMHS_H8(1, 0, 17) \
	VCMHS_H8(2, 0, 18) \
	VCMHS_H8(3, 0, 19) \
	VCMHS_H8(4, 0, 20) \
	VCMHS_H8(5, 0, 21) \
	VCMHS_H8(6, 0, 22) \
	VCMHS_H8(7, 0, 23) \
	VCMHS_H8(8, 0, 24)

// COUNT counts the all-ones lanes of V9-V16 into R4 and of V17-V24 into R5,
// clobbering those vectors. An all-ones lane is -1, so the lane sums are
// negated into counts.
#define COUNT \
	VADD V10.H8, V9.H8, V9.H8 \
	VADD V12.H8, V11.H8, V11.H8 \
	VADD V14.H8, V13.H8, V13.H8 \
	VADD V16.H8, V15.H8, V15.H8 \
	VADD V18.H8, V17.H8, V17.H8 \
	VADD V20.H8, V19.H8, V19.H8 \
	VADD V22.H8, V21.H8, V21.H8 \
	VADD V24.H8, V23.H8, V23.H8 \
	VADD V11.H8, V9.H8, V9.H8 \
	VADD V15.H8, V13.H8, V13.H8 \
	VADD V19.H8, V17.H8, V17.H8 \
	VADD V23.H8, V21.H8, V21.H8 \
	VADD V13.H8, V9.H8, V9.H8 \
	VADD V21.H8, V17.H8, V17.H8 \
	VADDV V9.H8, V9 \
	VADDV V17.H8, V17 \
	VMOV V9.H[0], R4 \
	VMOV V17.H[0], R5 \
	NEGW R4, R4 \
	NEGW R5, R5 \
	ANDW $0xffff, R4 \
	ANDW $0xffff, R5

// COLUMN leaves the bounds of the window in V0 among the first R3 slots of
// node R0's column in R4 and R5, clobbering R6 and V1-V24.
#define COLUMN \
	ADD $header_inline, R0, R6 \
	VLD1.P 64(R6), [V1.H8, V2.H8, V3.H8, V4.H8] \
	VLD1 (R6), [V5.H8, V6.H8, V7.H8, V8.H8] \
	BOUNDS \
	COUNT \
	CMP R3, R5 \
	CSEL HI, R3, R5, R5

TEXT ·columnBounds(SB), NOSPLIT, $0-40
	MOVD keys+0(FP), R6
	MOVHU target+8(FP), R1
	MOVD count+16(FP), R3
	VDUP R1, V0.H8
	VLD1.P 64(R6), [V1.H8, V2.H8, V3.H8, V4.H8]
	VLD1 (R6), [V5.H8, V6.H8, V7.H8, V8.H8]
	BOUNDS
	COUNT
	CMP R3, R5
	CSEL HI, R3, R5, R5
	MOVD R4, lo+24(FP)
	MOVD R5, hi+32(FP)
	RET

// SLOT32 leaves masks of the four-byte windows with high halves in Vh and
// low halves in Vl below the window (high half in V0, low in V31) in Vlt and
// of those at most it in Vle, clobbering Vh and Vg. A window is below if its
// high half is below, or equal with a lower low half.
#define SLOT32(h, l, lt, le, g, vh, vg, vlt, vle) \
	VCMHI_H8(h, 0, g) \
	VCMEQ V0.H8, vh.H8, vh.H8 \
	VCMHI_H8(l, 31, lt) \
	VCMHS_H8(l, 31, le) \
	VBIF vh.B16, vg.B16, vlt.B16 \
	VBIF vh.B16, vg.B16, vle.B16

// columnBounds32 is columnBounds for four-byte windows, whose high and low
// halves are in separate columns.
TEXT ·columnBounds32(SB), NOSPLIT, $0-48
	MOVD high+0(FP), R6
	MOVD low+8(FP), R7
	MOVWU target+16(FP), R1
	MOVD count+24(FP), R3
	LSRW $16, R1, R2
	VDUP R2, V0.H8
	VDUP R1, V31.H8
	VLD1.P 64(R6), [V1.H8, V2.H8, V3.H8, V4.H8]
	VLD1.P 64(R7), [V5.H8, V6.H8, V7.H8, V8.H8]
	SLOT32(1, 5, 9, 17, 25, V1, V25, V9, V17)
	SLOT32(2, 6, 10, 18, 26, V2, V26, V10, V18)
	SLOT32(3, 7, 11, 19, 27, V3, V27, V11, V19)
	SLOT32(4, 8, 12, 20, 28, V4, V28, V12, V20)
	VLD1 (R6), [V1.H8, V2.H8, V3.H8, V4.H8]
	VLD1 (R7), [V5.H8, V6.H8, V7.H8, V8.H8]
	SLOT32(1, 5, 13, 21, 25, V1, V25, V13, V21)
	SLOT32(2, 6, 14, 22, 26, V2, V26, V14, V22)
	SLOT32(3, 7, 15, 23, 27, V3, V27, V15, V23)
	SLOT32(4, 8, 16, 24, 28, V4, V28, V16, V24)
	COUNT
	CMP R3, R5
	CSEL HI, R3, R5, R5
	MOVD R4, lo+32(FP)
	MOVD R5, hi+40(FP)
	RET

// A node's branch pointer is the word 16 bytes before its header; children
// start the branch payload, and each child's header is 16 bytes into the
// child. TestNodeLayout checks this. The descents have no safe points, so
// the GC never observes the interior pointers they hold in registers.
//
// ConcurrentMap publishes children with release stores. The descents load
// them with plain loads, but every later load through a child depends on
// its address, which orders it after the child load on arm64.

// KEYWINDOW broadcasts the window of key bits R1 past node R0's prefix into
// V0, clobbering R6 and R7. An eight-byte prefix leaves a zero window.
#define KEYWINDOW \
	MOVD header_prefix(R0), R6 \
	LSL $3, R6 \
	LSL R6, R1, R7 \
	LSR $48, R7 \
	CMP $64, R6 \
	CSEL HS, ZR, R7, R7 \
	VDUP R7, V0.H8

// Numeric separators are raw eight-byte keys at this branch offset.
// TestNodeLayout checks both signed and unsigned instantiations.
#define BRANCH_KEYS 520

// descendBits uses R0 for the node's header, R1 for the key bits, R2 for
// its branch, and R3 for its count. It returns the leaf's header, a pointer
// into that node's allocation.
TEXT ·descendBits(SB), NOSPLIT, $0-56
	MOVD h+0(FP), R0
	MOVD bits+8(FP), R1
	MOVD ZR, lo+40(FP)
	MOVD ZR, hi+48(FP)
	MOVD -16(R0), R2
	CBZ R2, leaf

internal:
	MOVD header_count(R0), R3
	MOVD header_low(R0), R8
	MOVD header_mask(R0), R9
	EOR R1, R8, R4
	TST R9, R4
	BNE outside
	KEYWINDOW
	COLUMN
	CMP R4, R5
	BNE tie

step:
	MOVD (R2)(R4<<3), R0
	ADD $16, R0
	MOVD -16(R0), R2
	CBNZ R2, internal

leaf:
	MOVBU search+24(FP), R9
	CBNZ R9, search
	// The caller will lock this leaf. A lock serializes later loads, so
	// prefetch the leaf's numeric header and column lines and the payload
	// line holding its lock. Prefetches are hints, not reads of leaf data.
	// PRFM offsets are nonnegative, so they are relative to the node.
	SUB $16, R0, R6
	PRFM (R6), PLDL1KEEP
	PRFM 64(R6), PLDL1KEEP
	PRFM 128(R6), PLDL1KEEP
	MOVD -8(R0), R2
	PRFM (R2), PLDL1KEEP
	B done

search:
	MOVD header_count(R0), R3
	MOVD header_low(R0), R8
	MOVD header_mask(R0), R9
	EOR R1, R8, R4
	TST R9, R4
	BNE leafoutside
	KEYWINDOW
	COLUMN
	MOVD R4, lo+40(FP)
	MOVD R5, hi+48(FP)
	B done

	// A key outside the node's prefix sorts wholly before or after it.
outside:
	CMP R8, R1
	CSEL HI, R3, ZR, R4
	B step

leafoutside:
	CMP R8, R1
	CSEL HI, R3, ZR, R4
	MOVD R4, lo+40(FP)
	MOVD R4, hi+48(FP)

done:
	MOVD R0, node+32(FP)
	RET

	// Routing keys are immutable even for ConcurrentMap. The colliding
	// separators are slots R4 up to R5. Equality routes right; only a
	// separator strictly greater than the key stops this walk.
tie:
	SUB R4, R5, R5
	MOVD sign+16(FP), R9
	ADD $BRANCH_KEYS, R2, R10
	ADD R4<<3, R10, R10

separator:
	MOVD.P 8(R10), R6
	EOR R9, R6, R6
	CMP R1, R6
	BHI step
	ADD $1, R4
	SUBS $1, R5, R5
	BNE separator
	B step

// descendBatch has no calls or local frame. All 32 typed result slots are
// initialized, including unused lanes. Leaf data requires search=true.
// Registers are as in descendBits, plus R11 for the bits array, R12 for the
// active lanes, R13 for the lanes left in this pass, R14 for the lane, R15
// for the nodes array, and R19 for the bounds array.
TEXT ·descendBatch(SB), NOSPLIT, $0-608
	MOVD h+0(FP), R0
	MOVD $nodes+288(FP), R15
	MOVD $bounds+544(FP), R19
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	MOVD R15, R6
	VST1.P [V1.D2, V2.D2, V3.D2, V4.D2], 64(R6)
	VST1.P [V1.D2, V2.D2, V3.D2, V4.D2], 64(R6)
	VST1.P [V1.D2, V2.D2, V3.D2, V4.D2], 64(R6)
	VST1 [V1.D2, V2.D2, V3.D2, V4.D2], (R6)
	MOVD $-1, R6
	VDUP R6, V1.D2
	VDUP R6, V2.D2
	VDUP R6, V3.D2
	VDUP R6, V4.D2
	VST1 [V1.D2, V2.D2, V3.D2, V4.D2], (R19)
	MOVD count+272(FP), R8
	MOVD ZR, R6

batchroots:
	CMP R8, R6
	BHS batchstart
	MOVD R0, (R15)(R6<<3)
	ADD $1, R6
	B batchroots

batchstart:
	MOVD $bits+8(FP), R11
	MOVD $1, R12
	LSL R8, R12, R12
	SUB $1, R12

batchpass:
	MOVD R12, R13

batchlane:
	CBZ R13, batchnext
	RBIT R13, R14
	CLZ R14, R14
	SUB $1, R13, R6
	AND R6, R13, R13
	MOVD (R15)(R14<<3), R0
	MOVD -16(R0), R2
	CBNZ R2, batchroute
	MOVBU search+280(FP), R6
	CBZ R6, batchstop

batchroute:
	MOVD (R11)(R14<<3), R1
	MOVD header_count(R0), R3
	MOVD header_low(R0), R8
	MOVD header_mask(R0), R9
	EOR R1, R8, R4
	TST R9, R4
	BNE batchoutside
	KEYWINDOW
	COLUMN
	CBZ R2, batchfound
	CMP R4, R5
	BEQ batchstep
	// The colliding separators are slots R4 up to R5. Each one at most the
	// key routes it one child further right.
	SUB R4, R5, R5
	MOVD sign+264(FP), R9
	ADD $BRANCH_KEYS, R2, R10
	ADD R4<<3, R10, R10

batchseparator:
	MOVD.P 8(R10), R6
	EOR R9, R6, R6
	CMP R1, R6
	BHI batchstep
	ADD $1, R4
	SUBS $1, R5, R5
	BNE batchseparator
	B batchstep

batchoutside:
	CMP R8, R1
	CSEL HI, R3, ZR, R4
	MOVD R4, R5
	CBZ R2, batchfound

batchstep:
	MOVD (R2)(R4<<3), R6
	PRFM (R6), PLDL1KEEP
	PRFM 64(R6), PLDL1KEEP
	PRFM 128(R6), PLDL1KEEP
	ADD $16, R6, R0
	MOVD R0, (R15)(R14<<3)
	B batchlane

batchfound:
	ORR R5<<8, R4, R6
	MOVH R6, (R19)(R14<<1)

batchstop:
	MOVD $1, R6
	LSL R14, R6, R6
	BIC R6, R12, R12
	B batchlane

batchnext:
	CBNZ R12, batchpass
	RET

// getBits looks up key bits R1 from h for Map.Get and returns the matching
// entry or nil. A leaf's entries start entries bytes past its header, stride
// bytes apart, each with its 8-byte raw key word first. Leaves compare only
// equal windows, testing each candidate's raw key against raw. A window
// collision at an internal node counts the colliding separators at most the
// key, comparing them as ordered bits: raw^bits flips the sign of int64 keys.
TEXT ·getBits(SB), NOSPLIT, $0-48
	MOVD h+0(FP), R0
	MOVD bits+8(FP), R1
	MOVD ZR, entry+40(FP)
	MOVD -16(R0), R2
	CBZ R2, gleaf

ginternal:
	MOVD header_count(R0), R3
	MOVD header_low(R0), R8
	MOVD header_mask(R0), R9
	EOR R1, R8, R4
	TST R9, R4
	BNE goutside
	KEYWINDOW
	COLUMN
	CMP R4, R5
	BNE gtie

gstep:
	MOVD (R2)(R4<<3), R0
	ADD $16, R0
	MOVD -16(R0), R2
	CBNZ R2, ginternal

gleaf:
	MOVD header_count(R0), R3
	MOVD header_low(R0), R8
	MOVD header_mask(R0), R9
	EOR R1, R8, R4
	TST R9, R4
	BNE gout
	KEYWINDOW
	COLUMN
	CMP R4, R5
	BEQ gout
	MOVD raw+16(FP), R6
	MOVD entries+24(FP), R10
	MOVD stride+32(FP), R9
	ADD R0, R10, R10
	MADD R9, R10, R4, R10

gcandidate:
	MOVD (R10), R7
	CMP R6, R7
	BEQ gfound
	ADD R9, R10, R10
	ADD $1, R4
	CMP R4, R5
	BNE gcandidate

gout:
	RET

gfound:
	MOVD R10, entry+40(FP)
	RET

	// A key outside the node's prefix sorts wholly before or after it.
goutside:
	CMP R8, R1
	CSEL HI, R3, ZR, R4
	B gstep

	// The colliding separators are slots R4 up to R5. Each one at most the
	// key routes it one child further right.
gtie:
	SUB R4, R5, R5
	MOVD raw+16(FP), R9
	EOR R1, R9, R9
	ADD $BRANCH_KEYS, R2, R10
	ADD R4<<3, R10, R10

gseparator:
	MOVD.P 8(R10), R6
	EOR R9, R6, R6
	CMP R1, R6
	BHI gstep
	ADD $1, R4
	SUBS $1, R5, R5
	BNE gseparator
	B gstep

// prefetchLeaf hints the lines of a leaf's header, its column (the high
// halves, or both halves if wide), and its payload start, which holds the
// lock. The leaf pointer is the word 8 bytes before the header, which is 16
// bytes into the node.
TEXT ·prefetchLeaf(SB), NOSPLIT, $0-9
	MOVD h+0(FP), R0
	SUB $16, R0, R6
	PRFM (R6), PLDL1KEEP
	PRFM 64(R6), PLDL1KEEP
	PRFM 128(R6), PLDL1KEEP
	MOVBU wide+8(FP), R1
	CBZ R1, payload
	PRFM 192(R6), PLDL1KEEP
	PRFM 256(R6), PLDL1KEEP

payload:
	MOVD -8(R0), R2
	PRFM (R2), PLDL1KEEP
	RET

TEXT ·prefetchText(SB), NOSPLIT, $0-24
	MOVD key_base+0(FP), R0
	MOVD offset+16(FP), R1
	ADD R1, R0
	PRFM (R0), PLDL1KEEP
	RET
