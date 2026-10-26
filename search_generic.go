//go:build !(amd64 || arm64) || purego

package bbtree

import "unsafe"

func inlineBounds(keys *[capacity]uint16, target uint16, count int) (int, int) {
	lo, hi := scalarBounds(keys, target)
	return min(lo, count), min(hi, count)
}

func wideBounds(high, low *[capacity]uint16, target uint32, count int) (int, int) {
	lo, hi := scalarBounds32(high, low, target)
	return min(lo, count), min(hi, count)
}

const fastDescent = false

func descendBits(*header, uint64, uint64, bool) (node *header, lo, hi int) {
	panic("unreachable")
}

func descendBatch(*header, [lanes]uint64, uint64, int, bool) (nodes [lanes]*header, bounds [lanes]uint16) {
	panic("unreachable")
}

func getBits(*header, uint64, uint64, uintptr, uintptr) unsafe.Pointer {
	panic("unreachable")
}

func prefetchLeaf(*header, bool) {}

func prefetchText(string, int) {}
