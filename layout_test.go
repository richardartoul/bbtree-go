//go:build amd64 && !purego

package bbtree

import (
	"math"
	"math/rand/v2"
	"runtime"
	"testing"
	"unsafe"
)

// The assembly descent reads a node's branch and leaf pointers 16 and 8
// bytes before its header, and child pointers from the start of the branch
// payload.
func TestNodeLayout(t *testing.T) {
	check := func(name string, branch, leaf, header, children uintptr) {
		t.Helper()
		if branch != 0 || leaf != 8 || header != 16 || children != 0 {
			t.Errorf("%s: branch at %d, leaf at %d, header at %d, children at %d", name, branch, leaf, header, children)
		}
	}
	var a node[uint64, uint64]
	var b branch[uint64, uint64]
	check("uint64", unsafe.Offsetof(a.branch), unsafe.Offsetof(a.leaf), unsafe.Offsetof(a.header), unsafe.Offsetof(b.children))
	var c node[int64, [3]string]
	var d branch[int64, [3]string]
	check("int64", unsafe.Offsetof(c.branch), unsafe.Offsetof(c.leaf), unsafe.Offsetof(c.header), unsafe.Offsetof(d.children))
	if a, b := unsafe.Offsetof(b.keys), unsafe.Offsetof(d.keys); a != 520 || b != 520 {
		t.Errorf("separators at %d and %d, want 520", a, b)
	}
	if n := newLeaf[uint64, uint64](); leafOf(n) != n.leaf {
		t.Error("leaf payload is not where leafOf expects it")
	}
}

// TestDescendBits checks the assembly descents against route and bounds:
// leaves with and without search, window collisions at internal nodes,
// and keys outside a node's prefix. getBits must find exactly the
// entry a linear search of the routed leaf finds, including among several
// candidates.
func TestDescendBits(t *testing.T) {
	if !fastDescent {
		t.Skip("no AVX-512")
	}
	rng := rand.New(rand.NewPCG(1, 2))
	// Clusters share their high bytes, so separators' windows collide.
	key := func() uint64 {
		switch rng.IntN(4) {
		case 0:
			return rng.Uint64()
		case 1:
			return uint64(rng.IntN(64))<<56 | uint64(rng.IntN(1<<12))
		case 2:
			return 0xabcd<<48 | rng.Uint64()>>40
		default:
			return []uint64{0, 1, math.MaxUint64, 1 << 63}[rng.IntN(4)]
		}
	}
	var u Map[uint64, uint64]
	var s Map[int64, uint64]
	for range 100000 {
		k := key()
		u.Set(k, k)
		s.Set(int64(k), k)
	}
	exits := map[string]int{}
	for range 100000 {
		k := key()
		checkDescent(t, u.root, k, exits)
		checkDescent(t, s.root, int64(k), exits)
	}
	for _, exit := range []string{"leaf", "search", "collision", "outside", "candidates", "hit"} {
		if exits[exit] == 0 {
			t.Errorf("no %s exits: %v", exit, exits)
		}
	}
}

func checkDescent[K Key](t *testing.T, root *node[K, uint64], key K, exits map[string]int) {
	t.Helper()
	p := probeOf(key)
	leaf := root
	for leaf.branch != nil {
		if (p.bits^leaf.low)&leaf.mask != 0 {
			exits["outside"]++
		}
		if lo, hi := leaf.bounds(p); lo < hi {
			exits["collision"]++
		}
		leaf = leaf.route(p)
	}
	if (p.bits^leaf.low)&leaf.mask != 0 {
		exits["outside"]++
	}
	var want *entry[K, uint64]
	for i := range leaf.count {
		if e := &leafOf(leaf).entries[i]; e.key == key {
			want = e
			exits["hit"]++
		}
	}
	if lo, hi := leaf.bounds(p); hi-lo > 1 {
		exits["candidates"]++
	}
	var page *leafPage[K, uint64]
	entries := unsafe.Offsetof(page.leaf) + unsafe.Offsetof(page.leaf.entries) - unsafe.Offsetof(page.node.header)
	if got := getBits(&root.header, p.bits, *(*uint64)(unsafe.Pointer(&key)), entries, unsafe.Sizeof(page.leaf.entries[0])); got != unsafe.Pointer(want) {
		t.Fatalf("key %v: getBits found %p, want %p", key, got, want)
	}
	_, raw, _ := numericBits(key)
	var batch [lanes]uint64
	batch[0] = p.bits
	for _, search := range []bool{false, true} {
		headers, bounds := descendBatch(&root.header, batch, p.bits^raw, 1, search)
		if headers[0] != &leaf.header {
			t.Fatalf("key %v: batch did not reach the routed leaf", key)
		}
		wantBounds := uint16(0xffff)
		if search {
			lo, hi := leaf.bounds(p)
			wantBounds = uint16(lo | hi<<8)
		}
		if bounds[0] != wantBounds {
			t.Fatalf("key %v: batch bounds %x, want %x", key, bounds[0], wantBounds)
		}
		h, lo, hi := descendBits(&root.header, p.bits, p.bits^raw, search)
		got := nodeOf[K, uint64](h)
		if got != leaf {
			t.Fatalf("key %v: descent did not reach the routed leaf", key)
		}
		if search {
			if wlo, whi := leaf.bounds(p); lo != wlo || hi != whi {
				t.Fatalf("key %v: leaf range [%d, %d), want [%d, %d)", key, lo, hi, wlo, whi)
			}
			exits["search"]++
		} else {
			if lo != 0 || hi != 0 {
				t.Fatalf("key %v: leaf without search returned [%d, %d)", key, lo, hi)
			}
			exits["leaf"]++
		}
	}
}

// TestDescendHeaderKeepsNodeAlive retains only a header or entry pointer the
// assembly returns, churns the heap through GCs with allocations of the
// leaf's size, and then reads the leaf through that pointer.
func TestDescendHeaderKeepsNodeAlive(t *testing.T) {
	if !fastDescent {
		t.Skip("no AVX-512")
	}
	mul := uint64(0x9e3779b97f4a7c15)
	key := 1234 * mul
	want := entry[uint64, uint64]{key, 1234}
	var page *leafPage[uint64, uint64]
	entries := unsafe.Offsetof(page.leaf) + unsafe.Offsetof(page.leaf.entries) - unsafe.Offsetof(page.node.header)
	for round := range 30 {
		// Rounds retain a header from descendBits with or without search, or
		// an entry from getBits.
		var h *header
		var e unsafe.Pointer
		func() {
			var m Map[uint64, uint64]
			for i := uint64(0); i < 5000; i++ {
				m.Set(i*mul, i)
			}
			if round%3 == 2 {
				e = getBits(&m.root.header, key, key, entries, unsafe.Sizeof(page.leaf.entries[0]))
				return
			}
			h, _, _ = descendBits(&m.root.header, key, 0, round%3 == 1)
		}()
		var sink [][]byte
		for i := range 4096 {
			sink = append(sink, make([]byte, unsafe.Sizeof(leafPage[uint64, uint64]{})))
			if len(sink) > 64 {
				sink[0] = nil
				sink = sink[1:]
			}
			if i%1024 == 0 {
				runtime.GC()
			}
		}
		runtime.GC()
		if e != nil || round%3 == 2 {
			if e == nil || *(*entry[uint64, uint64])(e) != want {
				t.Fatalf("round %d: retained entry lost", round)
			}
		} else {
			n := nodeOf[uint64, uint64](h)
			lo, hi := n.bounds(probeOf(key))
			if hi-lo != 1 || leafOf(n).entries[lo] != want {
				t.Fatalf("round %d: recovered leaf lost entry: [%d, %d)", round, lo, hi)
			}
		}
		runtime.KeepAlive(sink)
	}
}
