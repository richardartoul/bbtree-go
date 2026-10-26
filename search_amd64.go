//go:build !purego

package bbtree

import "unsafe"

var hasAVX512 = avx512Supported()

// fastDescent selects the assembly descent for numeric keys.
var fastDescent = hasAVX512

// descendBits routes ordered key bits down from h through internal nodes
// without window collisions. It returns the header of the node where it
// stopped; done reports that the node is a leaf, otherwise Go routes it. With
// search set, it also returns the leaf's equal range [lo, hi) for the window;
// only callers that own the leaf may set it. It is not marked noescape: the
// result may alias h or point into another node reached from h.
func descendBits(h *header, bits uint64, search bool) (node *header, lo, hi int, done bool)

// getBits returns the entry of raw key word raw, with ordered bits, below h,
// or nil. The caller must own the tree or pass an already locked leaf.
// A leaf's entries start entries bytes past its header, stride bytes apart.
// Like descendBits, it is not noescape: the result points into a node reached
// from h.
func getBits(h *header, bits, raw uint64, entries, stride uintptr) (entry unsafe.Pointer)

// descendBatch interleaves numeric routing one level at a time. Each lane
// stops at a leaf, resolving separator collisions with raw key words XOR sign.
// Only a caller owning all leaves may set search. Bounds pack lo | hi<<8,
// or 0xffff when not searched.
// Pointer results retain their allocations, so this must not be noescape.
func descendBatch(h *header, bits [lanes]uint64, sign uint64, count int, search bool) (nodes [lanes]*header, bounds [lanes]uint16)

func avx512Supported() bool

//go:noescape
func columnBounds(keys *[capacity]uint16, target uint16, count int) (lo, hi int)

//go:noescape
func columnBounds32(high, low *[capacity]uint16, target uint32, count int) (lo, hi int)

// inlineBounds returns the equal range of target among the first count
// slots, using AVX-512 when available and SSE2 otherwise.
func inlineBounds(keys *[capacity]uint16, target uint16, count int) (int, int) {
	return columnBounds(keys, target, count)
}

// wideBounds is inlineBounds for four-byte windows split into halves.
func wideBounds(high, low *[capacity]uint16, target uint32, count int) (int, int) {
	return columnBounds32(high, low, target, count)
}

// prefetchLeaf hints a leaf's header, column, and lock lines into cache, so
// a batch can overlap the misses of its lanes.
//
//go:noescape
func prefetchLeaf(h *header, wide bool)

// prefetchText hints bytes already known to be within an immutable key.
// The caller must check offset < len(key).
//
//go:noescape
func prefetchText(key string, offset int)
