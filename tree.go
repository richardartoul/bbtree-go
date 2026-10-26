// Package bbtree implements cache-conscious, in-memory ordered maps inspired by
// github.com/XiangpengHao/bbtree. Map is single-threaded; ConcurrentMap is safe
// for concurrent use. Keys and values are copied, not deeply cloned.
package bbtree

import (
	"encoding/binary"
	"iter"
	"math/bits"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Key is an integer or bytewise-ordered string, including named types.
type Key interface {
	~int64 | ~uint64 | ~string
}

const capacity = 64

// lanes is the number of lookups GetMany interleaves.
const lanes = 32

// The header is beside the inline column, not behind the full keys. Header
// and payload are allocated together. Internal nodes have no entries. Nodes
// are not cache-line aligned. For uint64 keys, the pointers, routing fields,
// and numeric windows span the node's first 184 bytes.
type node[K Key, V any] struct {
	// The numeric assembly descent reads branch and leaf just before header
	// and expects children at the start of branch; tests check this layout.
	branch *branch[K, V]
	leaf   *leaf[K, V]
	header
	anchor K
	// On 64-bit Go, small scanned allocations have an eight-byte type
	// header. Keep eight-byte numeric key/value pairs on one cache line
	// without shifting the already-aligned numeric window column.
	_ [8]byte
}

// header holds the routing fields. It is not generic, so go_asm.h defines
// its field offsets for the assembly descent.
type header struct {
	count  int
	prefix int
	// fence is how many leading bytes every key routed here shares: the
	// common prefix of the node's separator fences, or zero at a border.
	fence int
	// mask selects the bits of low that a key must match to share the
	// prefix. Numeric nodes compare the ordered anchor encoding; string
	// nodes compare the anchor's eight bytes at max(fence, prefix-8).
	low, mask uint64
	// Slot i's window has its high half at inline[i] and its low half at
	// inlineLow[i]. Numeric windows are two bytes and use only high halves,
	// so numeric searches read just 128 bytes. Strings and named key types
	// use four-byte windows.
	inline    [capacity]uint16
	inlineLow [capacity]uint16
}

func (n *node[K, V]) entries() *[capacity]entry[K, V] {
	return &n.leaf.entries
}

type leafPage[K Key, V any] struct {
	node node[K, V]
	leaf leaf[K, V]
}

type branch[K Key, V any] struct {
	children [capacity + 1]*node[K, V]
	keys     [capacity]K
}

// childAt loads child i. A ConcurrentMap splitter replaces a published child
// in place with publishChild, so lock-free readers load children atomically.
// The assembly descents' aligned 8-byte loads of children are the same
// atomic, acquire-ordered loads on amd64. On arm64 they are plain loads, but
// every later load through a child depends on its address, which orders it.
func (b *branch[K, V]) childAt(i int) *node[K, V] {
	return (*node[K, V])(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&b.children[i]))))
}

// publishChild replaces child i with c, a fully built node with the same
// routing range. Only the ConcurrentMap structure mutex holder calls it.
func (b *branch[K, V]) publishChild(i int, c *node[K, V]) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&b.children[i])), unsafe.Pointer(c))
}

type entry[K Key, V any] struct {
	key   K
	value V
}

// The lock and fence precede the entries, so the descent can prefetch them
// with the header before a concurrent reader's lock serializes its loads.
type leaf[K Key, V any] struct {
	// These fields are used only by ConcurrentMap. A reader of an old routing
	// snapshot follows a split's high fence to the new right sibling.
	mu      sync.RWMutex
	high    K
	hasHigh bool
	next    *node[K, V]
	entries [capacity]entry[K, V]
}

func newLeaf[K Key, V any]() *node[K, V] {
	page := new(leafPage[K, V])
	page.node.leaf = &page.leaf
	return &page.node
}

func newBranch[K Key, V any]() *node[K, V] {
	page := new(struct {
		node   node[K, V]
		branch branch[K, V]
	})
	page.node.branch = &page.branch
	return &page.node
}

// word returns an order-preserving window at offset: two bytes in the high
// half for integers, four zero-padded bytes for strings. Named key types use
// the comparison fallback, so they retain their ordering without unsafe casts.
func word[K Key](key K, offset int) uint32 {
	if u, ok := ordered(key); ok {
		return uint32(u<<(8*uint(offset))>>48) << 16
	}
	if k, ok := any(key).(string); ok {
		return window4(k, offset)
	}
	return 0
}

func (n *node[K, V]) setSlot(i int, w uint32) {
	n.inline[i], n.inlineLow[i] = uint16(w>>16), uint16(w)
}

// moveSlots copies the windows of slots [from, from+count) to slot to.
// Numeric nodes never read low halves, so only wide windows move them.
func (n *node[K, V]) moveSlots(to, from, count int, wide bool) {
	copy(n.inline[to:to+count], n.inline[from:from+count])
	if wide {
		copy(n.inlineLow[to:to+count], n.inlineLow[from:from+count])
	}
}

func commonPrefix[K Key](a, b K) int {
	switch x := any(a).(type) {
	case uint64:
		return bits.LeadingZeros64(x^any(b).(uint64)) / 8
	case int64:
		return bits.LeadingZeros64(uint64(x^any(b).(int64))) / 8
	case string:
		y := any(b).(string)
		i := 0
		for i+8 <= len(x) && i+8 <= len(y) {
			d := binary.LittleEndian.Uint64([]byte(x[i:i+8])) ^ binary.LittleEndian.Uint64([]byte(y[i:i+8]))
			if d != 0 {
				return i + bits.TrailingZeros64(d)/8
			}
			i += 8
		}
		for i < len(x) && i < len(y) && x[i] == y[i] {
			i++
		}
		return i
	}
	return 0
}

func (n *node[K, V]) reindex() {
	if n.count == 0 {
		n.prefix = 0
	} else {
		n.anchor = n.key(0)
		n.prefix = commonPrefix(n.anchor, n.key(n.count-1))
	}
	n.low, n.mask = 0, 0
	if u, ok := ordered(n.anchor); ok {
		n.low, n.mask = u, ^uint64(0)<<(64-8*uint(n.prefix))
	} else if a, ok := any(n.anchor).(string); ok && n.prefix > n.fence {
		base := max(n.fence, n.prefix-8)
		n.low, n.mask = window8(a, base), ^uint64(0)<<(64-8*uint(n.prefix-base))
	}
	for i := 0; i < n.count; i++ {
		n.setSlot(i, word(n.key(i), n.prefix))
	}
	for i := n.count; i < capacity; i++ {
		n.setSlot(i, ^uint32(0))
	}
}

func (n *node[K, V]) key(i int) K {
	if n.branch != nil {
		return n.branch.keys[i]
	}
	return n.entries()[i].key
}

// ordered returns the order-preserving uint64 encoding of a numeric key.
// Named key types report false and use the generic path below.
func ordered[K Key](key K) (uint64, bool) {
	bits, _, ok := numericBits(key)
	return bits, ok
}

// numericBits returns a numeric key's ordered encoding and its raw word.
func numericBits[K Key](key K) (bits, raw uint64, ok bool) {
	if k, ok := any(key).(uint64); ok {
		return k, k, true
	}
	if k, ok := any(key).(int64); ok {
		return uint64(k) ^ (1 << 63), uint64(k), true
	}
	return 0, 0, false
}

// probe carries a search key and, for uint64 and int64 keys, its ordered
// encoding, so each node routes with integer operations alone.
type probe[K Key] struct {
	key     K
	bits    uint64
	numeric bool
}

func probeOf[K Key](key K) probe[K] {
	bits, numeric := ordered(key)
	return probe[K]{key, bits, numeric}
}

// bounds finds the inline collision range. Full comparisons resolve ties,
// including embedded NULs and strings that end inside a padded window.
func (n *node[K, V]) bounds(p probe[K]) (int, int) {
	if p.numeric {
		// A key outside the node's prefix sorts wholly before or after it.
		if (p.bits^n.low)&n.mask == 0 {
			return inlineBounds(&n.inline, uint16(p.bits<<(8*uint(n.prefix))>>48), n.count)
		}
		if p.bits < n.low {
			return 0, 0
		}
		return n.count, n.count
	}
	return n.textBounds(p.key)
}

// textBounds checks only prefix bytes that routing has not established:
// up to eight inline, and earlier ones against the anchor, which is usually
// needed only at border nodes such as the root.
func (n *node[K, V]) textBounds(key K) (int, int) {
	if n.count == 0 {
		return 0, 0
	}
	k, ok := any(key).(string)
	if !ok {
		// Named keys use zero windows and full comparisons to resolve ties.
		return wideBounds(&n.inline, &n.inlineLow, 0, n.count)
	}
	if n.prefix > n.fence {
		base := max(n.fence, n.prefix-8)
		if base > n.fence && !matchesPrefix(any(n.anchor).(string), k, n.fence, base) {
			if key < n.anchor {
				return 0, 0
			}
			return n.count, n.count
		}
		// Zero padding preserves order: a key ending inside the window sorts
		// before longer keys sharing its bytes, as a lower padded window does.
		if w := window8(k, base); (w^n.low)&n.mask != 0 {
			if w < n.low {
				return 0, 0
			}
			return n.count, n.count
		}
	}
	return wideBounds(&n.inline, &n.inlineLow, window4(k, n.prefix), n.count)
}

// matchesPrefix reports whether string key has anchor's bytes [from, to).
func matchesPrefix(anchor, key string, from, to int) bool {
	return len(key) >= to && anchor[from:to] == key[from:to]
}

// window4 returns four zero-padded big-endian bytes of s at offset.
func window4(s string, offset int) uint32 {
	if offset+4 <= len(s) {
		return binary.BigEndian.Uint32([]byte(s[offset : offset+4]))
	}
	var w uint32
	for i := offset; i < offset+4; i++ {
		w <<= 8
		if i < len(s) {
			w |= uint32(s[i])
		}
	}
	return w
}

// window8 returns eight zero-padded big-endian bytes of s at offset.
func window8(s string, offset int) uint64 {
	if offset+8 <= len(s) {
		return binary.BigEndian.Uint64([]byte(s[offset : offset+8]))
	}
	var w uint64
	for i := offset; i < offset+8; i++ {
		w <<= 8
		if i < len(s) {
			w |= uint64(s[i])
		}
	}
	return w
}

// fences bound the keys that can route to a node; nil means unbounded.
type fences[K Key] struct{ low, high *K }

func (f fences[K]) prefix() int {
	if f.low == nil || f.high == nil {
		return 0
	}
	return commonPrefix(*f.low, *f.high)
}

// childFences returns the fences of internal node n's child i.
func childFences[K Key, V any](f fences[K], n *node[K, V], i int) fences[K] {
	if i > 0 {
		f.low = &n.branch.keys[i-1]
	}
	if i < n.count {
		f.high = &n.branch.keys[i]
	}
	return f
}

// Exact lookups do not need a lower bound. A unique inline candidate requires
// just equality, loading its key and value together from the entry column.
// Numeric window searches are written out here, in bounds, and in route,
// because a shared helper exceeds the inlining budget.
func (n *node[K, V]) lookup(p probe[K]) (V, bool) {
	var lo, hi int
	if !p.numeric {
		lo, hi = n.textBounds(p.key)
	} else if (p.bits^n.low)&n.mask == 0 {
		lo, hi = inlineBounds(&n.inline, uint16(p.bits<<(8*uint(n.prefix))>>48), n.count)
	}
	return n.candidates(p.key, lo, hi)
}

// windowEqual compares keys whose zero-padded prefix and four-byte window
// already match. Lengths still distinguish strings ending in zero bytes.
func windowEqual[K Key](query, stored K, skip int) bool {
	q, ok := any(query).(string)
	if !ok {
		return query == stored
	}
	s := any(stored).(string)
	return len(q) == len(s) && (skip >= len(q) || q[skip:] == s[skip:])
}

// candidates finds key among the leaf entries [lo, hi) whose windows match.
func (n *node[K, V]) candidates(key K, lo, hi int) (V, bool) {
	if hi-lo == 1 {
		if e := &n.leaf.entries[lo]; e.key == key {
			return e.value, true
		}
	} else if lo < hi {
		return n.collision(key, lo, hi)
	}
	var zero V
	return zero, false
}

//go:noinline
func (n *node[K, V]) collision(key K, lo, hi int) (V, bool) {
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		e := &n.leaf.entries[mid]
		if e.key == key {
			return e.value, true
		}
		if e.key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var zero V
	return zero, false
}

func (n *node[K, V]) lower(p probe[K]) int {
	lo, hi := n.bounds(p)
	return n.narrow(p.key, lo, hi)
}

// narrow finds key's lower bound among the leaf entries [lo, hi) whose
// windows match.
func (n *node[K, V]) narrow(key K, lo, hi int) int {
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if n.entries()[mid].key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (n *node[K, V]) child(p probe[K]) int {
	lo, hi := n.bounds(p)
	if lo < hi {
		lo = n.tie(p.key, lo, hi)
	}
	return lo
}

// tie resolves an inline collision with full separator comparisons.
//
//go:noinline
func (n *node[K, V]) tie(key K, lo, hi int) int {
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if key < n.branch.keys[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// route returns the child of internal node n that covers p.
func (n *node[K, V]) route(p probe[K]) *node[K, V] {
	var lo int
	if !p.numeric {
		var hi int
		lo, hi = n.textBounds(p.key)
		if lo < hi {
			lo = n.tie(p.key, lo, hi)
		}
	} else if (p.bits^n.low)&n.mask == 0 {
		var hi int
		lo, hi = inlineBounds(&n.inline, uint16(p.bits<<(8*uint(n.prefix))>>48), n.count)
		if lo < hi {
			lo = n.tie(p.key, lo, hi)
		}
	} else if p.bits > n.low {
		lo = n.count
	}
	return n.branch.childAt(lo)
}

// descend routes numeric keys through internal levels in one assembly call
// when available. Other architectures and race builds use route.
func descend[K Key, V any](n *node[K, V], p probe[K]) *node[K, V] {
	if n != nil && p.numeric && fastDescent && !raceEnabled {
		if n.branch != nil {
			_, raw, _ := numericBits(p.key)
			h, _, _ := descendBits(&n.header, p.bits, p.bits^raw, false)
			return nodeOf[K, V](h)
		}
		return n
	}
	for n != nil && n.branch != nil {
		n = n.route(p)
	}
	return n
}

// leafOf returns leaf node n's payload. Every leaf is the node field at the
// start of a leafPage, so the payload is in the same allocation at a fixed
// offset, and reaching it loads neither n.leaf nor the payload's first line.
func leafOf[K Key, V any](n *node[K, V]) *leaf[K, V] {
	return &(*leafPage[K, V])(unsafe.Pointer(n)).leaf
}

// nodeOf returns the node containing header h. The assembly descent returns
// only headers of nodes it reached from the caller's node[K, V], so h points
// into such a node, and the result stays within the same allocation.
func nodeOf[K Key, V any](h *header) *node[K, V] {
	var n node[K, V]
	return (*node[K, V])(unsafe.Add(unsafe.Pointer(h), -int(unsafe.Offsetof(n.header))))
}

// numericEntry bridges the generic entry layout to the numeric assembly
// lookup. The caller owns the tree, or holds n's leaf lock and n is a leaf.
func (n *node[K, V]) numericEntry(bits, raw uint64) *entry[K, V] {
	var page *leafPage[K, V]
	entries := unsafe.Offsetof(page.leaf) + unsafe.Offsetof(page.leaf.entries) - unsafe.Offsetof(page.node.header)
	return (*entry[K, V])(getBits(&n.header, bits, raw, entries, unsafe.Sizeof(page.leaf.entries[0])))
}

// Map is an ordered map for one goroutine at a time. Its zero value is empty.
// Do not copy a nonempty Map. Iteration must not overlap mutation.
type Map[K Key, V any] struct {
	root *node[K, V]
	last *node[K, V]
	len  int
}

func (m *Map[K, V]) Len() int { return m.len }

// Get returns the value and whether key is present.
func (m *Map[K, V]) Get(key K) (V, bool) {
	bits, raw, numeric := numericBits(key)
	if n := m.root; n != nil && numeric && fastDescent {
		// One assembly call finds the entry, comparing raw 8-byte key words
		// for candidates and separators whose windows collide.
		if e := n.numericEntry(bits, raw); e != nil {
			return e.value, true
		}
		var zero V
		return zero, false
	}
	p := probe[K]{key, bits, numeric}
	if n, lo, hi := m.seek(p); n != nil {
		return n.candidates(key, lo, hi)
	}
	var zero V
	return zero, false
}

// seek returns the leaf p routes to, or nil if the map is empty, and the
// range of entries there whose windows equal p's. For numeric keys, one
// assembly call routes to the leaf and searches its column.
func (m *Map[K, V]) seek(p probe[K]) (n *node[K, V], lo, hi int) {
	n = m.root
	if n == nil {
		return
	}
	if p.numeric && fastDescent {
		_, raw, _ := numericBits(p.key)
		h, lo, hi := descendBits(&n.header, p.bits, p.bits^raw, true)
		return nodeOf[K, V](h), lo, hi
	}
	n = descend(n, p)
	lo, hi = n.bounds(p)
	return
}

// Set inserts or replaces a value, returning the previous value and presence.
func (m *Map[K, V]) Set(key K, value V) (V, bool) {
	if m.root == nil {
		m.root = newLeaf[K, V]()
		m.root.reindex()
		m.last = m.root
	}
	p := probeOf(key)
	if n := m.last; n.count > 0 && n.count < capacity && key > n.entries()[n.count-1].key {
		n.put(n.count, p, value)
		m.len++
		var zero V
		return zero, false
	}
	n, lo, hi := m.seek(p)
	i := n.narrow(key, lo, hi)
	if i < hi && n.entries()[i].key == key {
		old := n.entries()[i].value
		n.entries()[i].value = value
		return old, true
	}
	if n.count < capacity {
		n.put(i, p, value)
		m.len++
		var zero V
		return zero, false
	}
	// Only a full leaf needs the recursive path that propagates splits.
	old, replaced, right, sep := insert(m.root, p, value, fences[K]{})
	if right != nil {
		m.root = newRoot(m.root, right, sep)
	}
	if !replaced {
		m.len++
	}
	if m.last.leaf.next != nil {
		m.last = m.last.leaf.next
	}
	return old, replaced
}

func newRoot[K Key, V any](left, right *node[K, V], sep K) *node[K, V] {
	n := newBranch[K, V]()
	n.count = 1
	n.branch.keys[0] = sep
	n.branch.children[0], n.branch.children[1] = left, right
	n.reindex()
	return n
}

// insert only changes a branch when a child splits. f bounds n's keys.
func insert[K Key, V any](n *node[K, V], p probe[K], value V, f fences[K]) (old V, replaced bool, right *node[K, V], sep K) {
	key := p.key
	if n.branch == nil {
		lo, hi := n.bounds(p)
		i := n.narrow(key, lo, hi)
		if i < hi && n.entries()[i].key == key {
			old, n.entries()[i].value = n.entries()[i].value, value
			return old, true, nil, sep
		}
		if n.count == capacity {
			right = splitLeaf(n, f)
			sep = right.entries()[0].key
			if key >= sep {
				right.put(right.lower(p), p, value)
			} else {
				n.put(i, p, value)
			}
			return old, false, right, sep
		}
		n.put(i, p, value)
		return old, false, nil, sep
	}
	i := n.child(p)
	old, replaced, right, sep = insert(n.branch.children[i], p, value, childFences(f, n, i))
	if right == nil {
		return
	}
	if n.count < capacity {
		n.putChild(i, sep, right)
		return old, replaced, nil, sep
	}
	return splitBranch(n, i, sep, right, old, replaced, f)
}

// shares reports whether a key routed to n has n's prefix, reading string
// bytes only for border nodes, as textBounds does.
func (n *node[K, V]) shares(p probe[K]) bool {
	if p.numeric {
		return (p.bits^n.low)&n.mask == 0
	}
	if n.prefix <= n.fence {
		return true
	}
	k, _ := any(p.key).(string)
	base := max(n.fence, n.prefix-8)
	return len(k) >= n.prefix && (base == n.fence || matchesPrefix(any(n.anchor).(string), k, n.fence, base)) &&
		(window8(k, base)^n.low)&n.mask == 0
}

// put inserts p's key at i in a leaf that p routes to.
func (n *node[K, V]) put(i int, p probe[K], value V) {
	key := p.key
	refit := n.count == 0 || !n.shares(p)
	n.moveSlots(i+1, i, n.count-i, !p.numeric)
	copy(n.entries()[i+1:n.count+1], n.entries()[i:n.count])
	n.entries()[i] = entry[K, V]{key, value}
	n.count++
	if refit {
		n.reindex()
	} else {
		n.setSlot(i, word(key, n.prefix))
	}
}

func splitLeaf[K Key, V any](n *node[K, V], f fences[K]) *node[K, V] {
	r := newLeaf[K, V]()
	mid := capacity / 2
	r.count = n.count - mid
	copy(r.entries()[:], n.entries()[mid:n.count])
	clear(n.entries()[mid:n.count])
	n.count = mid
	r.leaf.next, n.leaf.next = n.leaf.next, r
	r.leaf.high, r.leaf.hasHigh = n.leaf.high, n.leaf.hasHigh
	n.leaf.high, n.leaf.hasHigh = r.entries()[0].key, true
	sep := &r.entries()[0].key
	n.fence, r.fence = fences[K]{f.low, sep}.prefix(), fences[K]{sep, f.high}.prefix()
	n.reindex()
	r.reindex()
	return r
}

func (n *node[K, V]) putChild(i int, sep K, right *node[K, V]) {
	p := probeOf(sep)
	refit := n.count == 0 || !n.shares(p)
	n.moveSlots(i+1, i, n.count-i, !p.numeric)
	copy(n.branch.keys[i+1:n.count+1], n.branch.keys[i:n.count])
	copy(n.branch.children[i+2:n.count+2], n.branch.children[i+1:n.count+1])
	n.branch.keys[i], n.branch.children[i+1] = sep, right
	n.count++
	if refit {
		n.reindex()
	} else {
		n.setSlot(i, word(sep, n.prefix))
	}
}

func splitBranch[K Key, V any](n *node[K, V], i int, sep K, child *node[K, V], old V, replaced bool, f fences[K]) (V, bool, *node[K, V], K) {
	var keys [capacity + 1]K
	var children [capacity + 2]*node[K, V]
	copy(keys[:i], n.branch.keys[:i])
	keys[i] = sep
	copy(keys[i+1:], n.branch.keys[i:n.count])
	copy(children[:i+1], n.branch.children[:i+1])
	children[i+1] = child
	copy(children[i+2:], n.branch.children[i+1:n.count+1])
	mid := capacity / 2
	r := newBranch[K, V]()
	r.count = capacity - mid
	clear(n.branch.keys[:])
	clear(n.branch.children[:])
	n.count = mid
	copy(n.branch.keys[:], keys[:mid])
	copy(n.branch.children[:], children[:mid+1])
	copy(r.branch.keys[:], keys[mid+1:])
	copy(r.branch.children[:], children[mid+1:])
	n.fence, r.fence = fences[K]{f.low, &keys[mid]}.prefix(), fences[K]{&keys[mid], f.high}.prefix()
	n.reindex()
	r.reindex()
	return old, replaced, r, keys[mid]
}

// Delete removes key, returning its previous value and presence. Sparse nodes,
// including empty leaves, are retained until Compact or Clear.
func (m *Map[K, V]) Delete(key K) (V, bool) {
	p := probeOf(key)
	n, lo, hi := m.seek(p)
	if n == nil {
		var zero V
		return zero, false
	}
	old, ok := n.remove(n.narrow(key, lo, hi), p)
	if ok {
		m.len--
	}
	return old, ok
}

// remove deletes p's key if it is at i, its lower bound.
func (n *node[K, V]) remove(i int, p probe[K]) (old V, ok bool) {
	if i == n.count || n.entries()[i].key != p.key {
		return
	}
	old = n.entries()[i].value
	n.moveSlots(i, i+1, n.count-1-i, !p.numeric)
	copy(n.entries()[i:n.count-1], n.entries()[i+1:n.count])
	n.count--
	n.entries()[n.count] = entry[K, V]{}
	n.setSlot(n.count, ^uint32(0))
	return old, true
}

// Clear releases all entries. Values retained by callers remain valid.
func (m *Map[K, V]) Clear() { m.root, m.last, m.len = nil, nil, 0 }

// All visits entries in ascending key order. Returning false stops iteration.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		n := m.root
		for n != nil && n.branch != nil {
			n = n.branch.children[0]
		}
		for ; n != nil; n = n.leaf.next {
			for i := 0; i < n.count; i++ {
				if !yield(n.entries()[i].key, n.entries()[i].value) {
					return
				}
			}
		}
	}
}

// Range visits start <= key < end. Bounds are literal; use All for an
// unbounded traversal. Returning false stops iteration.
func (m *Map[K, V]) Range(start, end K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if start >= end {
			return
		}
		p, q := probeOf(start), probeOf(end)
		n := descend(m.root, p)
		if n == nil {
			return
		}
		// Locate a string end once instead of comparing every entry with it,
		// which would read each key's bytes. Iteration excludes mutation, so
		// the position stays valid. Numeric comparisons are cheaper than that
		// extra descent.
		var last *node[K, V]
		stop := 0
		if !q.numeric {
			last = descend(m.root, q)
			stop = last.lower(q)
		}
		for i := n.lower(p); n != nil; n, i = n.leaf.next, 0 {
			limit := n.count
			if n == last {
				limit = stop
			}
			for ; i < limit; i++ {
				e := &n.entries()[i]
				if last == nil && e.key >= end || !yield(e.key, e.value) {
					return
				}
			}
			if n == last {
				return
			}
		}
	}
}

// Compact rebuilds densely packed nodes, reclaiming space left by deletions.
// It uses O(n) temporary space and does not change the entries.
func (m *Map[K, V]) Compact() {
	if m.len == 0 {
		m.Clear()
		return
	}
	type levelNode struct {
		node *node[K, V]
		min  K
	}
	// Each node's fences are its own minimum and its right neighbor's.
	seal := func(level []levelNode) {
		for i := range level {
			var f fences[K]
			if i > 0 {
				f.low = &level[i].min
			}
			if i+1 < len(level) {
				f.high = &level[i+1].min
			}
			level[i].node.fence = f.prefix()
			level[i].node.reindex()
		}
	}
	level := make([]levelNode, 0, (m.len+capacity-1)/capacity)
	n := newLeaf[K, V]()
	for k, v := range m.All() {
		if n.count == capacity {
			level = append(level, levelNode{n, n.entries()[0].key})
			next := newLeaf[K, V]()
			n.leaf.next, n.leaf.high, n.leaf.hasHigh = next, k, true
			n = next
		}
		n.entries()[n.count] = entry[K, V]{k, v}
		n.count++
	}
	level = append(level, levelNode{n, n.entries()[0].key})
	seal(level)
	m.last = n
	for len(level) > 1 {
		parents := make([]levelNode, 0, (len(level)+capacity)/(capacity+1))
		for base := 0; base < len(level); base += capacity + 1 {
			children := level[base:min(base+capacity+1, len(level))]
			n = newBranch[K, V]()
			n.count = len(children) - 1
			for i, child := range children {
				n.branch.children[i] = child.node
				if i > 0 {
					n.branch.keys[i-1] = child.min
				}
			}
			parents = append(parents, levelNode{n, children[0].min})
		}
		seal(parents)
		level = parents
	}
	m.root = level[0].node
}

// GetMany fills values and found in input order. Outputs must be at least as
// long as keys. The caller owns the buffers; the operation allocates nothing.
func (m *Map[K, V]) GetMany(keys []K, values []V, found []bool) {
	if len(values) < len(keys) || len(found) < len(keys) {
		panic("bbtree: short GetMany output")
	}
	if !m.root.batchable() {
		for i, key := range keys {
			values[i], found[i] = m.Get(key)
		}
		return
	}
	for base := 0; base < len(keys); base += lanes {
		count := min(lanes, len(keys)-base)
		var probes [lanes]probe[K]
		var nodes [lanes]*node[K, V]
		for i := 0; i < count; i++ {
			probes[i] = probeOf(keys[base+i])
		}
		var ranges [lanes][2]int
		descendLanes(m.root, &probes, &nodes, count, &ranges)
		if nodes[0] == nil {
			clear(values[base : base+count])
			clear(found[base : base+count])
			continue
		}
		// Fetch candidate descriptors independently before comparing string
		// bytes. Numeric values usually share the candidate key's cache line.
		var candidates [lanes]K
		for i := 0; i < count; i++ {
			if ranges[i][1]-ranges[i][0] == 1 {
				e := &nodes[i].leaf.entries[ranges[i][0]]
				candidates[i], values[base+i] = e.key, e.value
			}
		}
		if texts, ok := any(&candidates).(*[lanes]string); ok {
			for i := 0; i < count; i++ {
				if offset := nodes[i].prefix + 4; offset < len(texts[i]) {
					prefetchText(texts[i], offset)
				}
			}
		}
		for i := 0; i < count; i++ {
			lo, hi := ranges[i][0], ranges[i][1]
			if hi-lo == 1 {
				found[base+i] = windowEqual(probes[i].key, candidates[i], nodes[i].prefix+4)
				if !found[base+i] {
					var zero V
					values[base+i] = zero
				}
			} else {
				values[base+i], found[base+i] = nodes[i].candidates(probes[i].key, lo, hi)
			}
		}
	}
}

// batchable follows upstream's concurrent cutoff: below three internal
// levels, lane bookkeeping costs more than the memory latency it overlaps.
// Only immutable node identities are inspected, not mutable leaf headers.
func (n *node[K, V]) batchable() bool {
	return n != nil && n.branch != nil && n.branch.childAt(0).branch != nil &&
		n.branch.childAt(0).branch.childAt(0).branch != nil
}

// descendLanes routes count probes from root to their leaves, interleaving
// independent descents level by level without goroutines or allocations.
// Only an unsynchronized map may request leaf column searches via ranges.
func descendLanes[K Key, V any](root *node[K, V], probes *[lanes]probe[K], nodes *[lanes]*node[K, V], count int, ranges *[lanes][2]int) {
	if count == 0 || root == nil {
		return
	}
	if fastDescent && !raceEnabled && probes[0].numeric && count >= 8 && root.branch != nil {
		var bits [lanes]uint64
		for i := 0; i < count; i++ {
			bits[i] = probes[i].bits
		}
		_, raw, _ := numericBits(probes[0].key)
		headers, bounds := descendBatch(&root.header, bits, bits[0]^raw, count, ranges != nil)
		for i := 0; i < count; i++ {
			nodes[i] = nodeOf[K, V](headers[i])
			if ranges != nil {
				ranges[i][0], ranges[i][1] = int(bounds[i]&255), int(bounds[i]>>8)
			}
		}
		if ranges != nil {
			return
		}
	} else {
		for i := 0; i < count; i++ {
			nodes[i] = root
		}
		if !probes[0].numeric {
			for nodes[0].branch != nil {
				for i := 0; i < count; i++ {
					n := nodes[i]
					lo, hi := n.textBounds(probes[i].key)
					if lo < hi {
						lo = n.tie(probes[i].key, lo, hi)
					}
					nodes[i] = n.branch.childAt(lo)
				}
			}
		} else {
			for nodes[0].branch != nil {
				for i := 0; i < count; i++ {
					nodes[i] = nodes[i].route(probes[i])
				}
			}
		}
	}
	// Issue every lane's leaf misses before any lane searches or locks a
	// leaf; a concurrent reader's lock would otherwise serialize them.
	for i := 0; i < count; i++ {
		prefetchLeaf(&nodes[i].header, !probes[i].numeric)
	}
	if ranges != nil {
		for i := 0; i < count; i++ {
			ranges[i][0], ranges[i][1] = nodes[i].bounds(probes[i])
		}
	}
}
