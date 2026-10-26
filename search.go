package bbtree

// The portable search is also the reference for architecture-specific paths.
// Go does not autovectorize this loop; amd64 supplies an SSE2 implementation.
func scalarBounds(keys *[capacity]uint16, target uint16) (lo, hi int) {
	for lo < capacity && keys[lo] < target {
		lo++
	}
	hi = lo
	for hi < capacity && keys[hi] == target {
		hi++
	}
	return
}

// scalarBounds32 searches four-byte windows stored as high and low halves.
func scalarBounds32(high, low *[capacity]uint16, target uint32) (lo, hi int) {
	at := func(i int) uint32 { return uint32(high[i])<<16 | uint32(low[i]) }
	for lo < capacity && at(lo) < target {
		lo++
	}
	hi = lo
	for hi < capacity && at(hi) == target {
		hi++
	}
	return
}
