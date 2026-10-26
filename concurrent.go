package bbtree

import (
	"iter"
	"sync"
	"sync/atomic"
)

// ConcurrentMap is safe for concurrent readers and writers. Its zero value is
// empty. Do not copy it after first use. Individual operations are linearizable;
// batches and scans are not transactions. As in upstream bbtree, there is no
// shared length counter. Deleted slots are reused, but nodes are not merged.
//
// Routing keys are immutable; child pointers publish replacement nodes
// atomically. Leaves have individual locks; only splits serialize on the
// structure mutex. Go's GC retains nodes used by older routing snapshots.
type ConcurrentMap[K Key, V any] struct {
	root      atomic.Pointer[node[K, V]]
	structure sync.Mutex
}

// readLeaf returns a shared-locked leaf. A split can occur after descent, so
// follow high fences until reaching the leaf that currently owns key.
func readLeaf[K Key, V any](n *node[K, V], key K) *node[K, V] {
	for n != nil {
		n.leaf.mu.RLock()
		if !n.leaf.hasHigh || key < n.leaf.high {
			return n
		}
		next := n.leaf.next
		n.leaf.mu.RUnlock()
		n = next
	}
	return nil
}

// writeLeaf reaches the lock through the enclosing leaf allocation, avoiding
// a dependent load of the leaf pointer from the node's routing cache line.
func writeLeaf[K Key, V any](n *node[K, V], key K) *node[K, V] {
	for n != nil {
		l := leafOf(n)
		l.mu.Lock()
		if !l.hasHigh || key < l.high {
			return n
		}
		n = l.next
		l.mu.Unlock()
	}
	return nil
}

// Get returns a copy of the stored value, not a deep clone. Mutable objects
// referenced by a value need their own synchronization.
func (m *ConcurrentMap[K, V]) Get(key K) (V, bool) {
	bits, raw, numeric := numericBits(key)
	p := probe[K]{key, bits, numeric}
	n := readLeaf(descend(m.root.Load(), p), key)
	if n != nil {
		if numeric && fastDescent {
			// readLeaf holds the destination's lock after split redirects.
			// Search only this locked leaf, never an unlocked root.
			e := n.numericEntry(bits, raw)
			var value V
			if e != nil {
				value = e.value
			}
			n.leaf.mu.RUnlock()
			return value, e != nil
		}
		value, ok := n.lookup(p)
		n.leaf.mu.RUnlock()
		return value, ok
	}
	var zero V
	return zero, false
}

// Set inserts or replaces a value, returning the previous value and presence.
func (m *ConcurrentMap[K, V]) Set(key K, value V) (V, bool) {
	p := probeOf(key)
	n := writeLeaf(descend(m.root.Load(), p), key)
	if n != nil {
		// An empty matching-window range proves absence without reading an entry.
		lo, hi := n.bounds(p)
		i := lo
		if hi-lo > 1 {
			i = n.narrow(key, lo, hi)
		}
		if i < hi && n.entries()[i].key == key {
			old := n.entries()[i].value
			n.entries()[i].value = value
			n.leaf.mu.Unlock()
			return old, true
		}
		if hi-lo == 1 && n.entries()[i].key < key {
			i++
		}
		if n.count < capacity {
			n.put(i, p, value)
			n.leaf.mu.Unlock()
			var zero V
			return zero, false
		}
		n.leaf.mu.Unlock()
	}
	return m.setSplit(p, value)
}

// setSplit rechecks the destination and inserts under the structure mutex.
func (m *ConcurrentMap[K, V]) setSplit(p probe[K], value V) (V, bool) {
	// Re-descend after taking the split mutex: another splitter may have
	// changed both the root and the destination while we were waiting.
	m.structure.Lock()
	defer m.structure.Unlock()
	root := m.root.Load()
	if root == nil {
		root = newLeaf[K, V]()
		root.reindex()
		root.put(0, p, value)
		m.root.Store(root)
		var zero V
		return zero, false
	}
	left, right, sep, old, replaced := insertShared(root, p, value, fences[K]{})
	if right != nil {
		left = newRoot(left, right, sep)
	}
	if left != root {
		m.root.Store(left)
	}
	return old, replaced
}

// insertShared inserts under the structure mutex. Routing nodes are
// immutable once published except for child pointers: a branch whose
// separators change is copied, and its parent publishes the copy in place.
// Ordinary inserts mutate one locked leaf and allocate nothing.
func insertShared[K Key, V any](n *node[K, V], p probe[K], value V, f fences[K]) (left, right *node[K, V], sep K, old V, replaced bool) {
	if n.branch == nil {
		n.leaf.mu.Lock()
		old, replaced, right, sep = insert(n, p, value, f)
		n.leaf.mu.Unlock()
		return n, right, sep, old, replaced
	}
	i := n.child(p)
	prev := n.branch.children[i]
	child, right, sep, old, replaced := insertShared(prev, p, value, childFences(f, n, i))
	if right == nil {
		// A copied child covers the same keys. Readers see the old or new
		// child; either routes correctly, and stale leaves redirect.
		if child != prev {
			n.branch.publishChild(i, child)
		}
		return n, nil, sep, old, replaced
	}
	left = newBranch[K, V]()
	branch := left.branch
	*left = *n
	left.branch = branch
	*left.branch = *n.branch
	left.branch.children[i] = child
	if left.count < capacity {
		left.putChild(i, sep, right)
		return left, nil, sep, old, replaced
	}
	old, replaced, right, sep = splitBranch(left, i, sep, right, old, replaced, f)
	return left, right, sep, old, replaced
}

// Delete removes an entry. Empty leaves retain their routing fences, so a
// concurrent lookup that already loaded an older root can still finish.
func (m *ConcurrentMap[K, V]) Delete(key K) (V, bool) {
	p := probeOf(key)
	n := writeLeaf(descend(m.root.Load(), p), key)
	if n == nil {
		var zero V
		return zero, false
	}
	old, ok := n.remove(n.lower(p), p)
	n.leaf.mu.Unlock()
	return old, ok
}

// Update atomically transforms an existing value. It does not insert missing
// keys. The callback runs under the leaf's exclusive lock; it must not call
// back into this map. A panic releases the lock without replacing the stored
// value; mutations to objects referenced by that value are not rolled back.
func (m *ConcurrentMap[K, V]) Update(key K, f func(V) V) bool {
	p := probeOf(key)
	n := writeLeaf(descend(m.root.Load(), p), key)
	if n == nil {
		return false
	}
	defer n.leaf.mu.Unlock()
	i := n.lower(p)
	if i == n.count || n.entries()[i].key != key {
		return false
	}
	n.entries()[i].value = f(n.entries()[i].value)
	return true
}

// Range visits start <= key < end in ascending order. It is not a snapshot,
// but visits each continuously present entry exactly once. The callback runs
// under a shared leaf lock and must not call back into this map. Panics release
// the lock. Returning false stops iteration.
func (m *ConcurrentMap[K, V]) Range(start, end K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if start >= end {
			return
		}
		p := probeOf(start)
		n := readLeaf(descend(m.root.Load(), p), start)
		if n == nil {
			return
		}
		defer func() {
			if n != nil {
				n.leaf.mu.RUnlock()
			}
		}()
		for i := n.lower(p); ; i = 0 {
			// Entries are below the leaf's high fence. When that is at most
			// end, skip per-entry comparisons, which read string key bytes.
			if n.leaf.hasHigh && n.leaf.high <= end {
				for ; i < n.count; i++ {
					if !yield(n.entries()[i].key, n.entries()[i].value) {
						return
					}
				}
			}
			for ; i < n.count; i++ {
				if n.entries()[i].key >= end || !yield(n.entries()[i].key, n.entries()[i].value) {
					return
				}
			}
			// Lock coupling prevents a split from moving visited entries into
			// the next leaf between releasing this lock and acquiring its next.
			next := n.leaf.next
			if next != nil {
				next.leaf.mu.RLock()
			}
			n.leaf.mu.RUnlock()
			n = next
			if n == nil {
				return
			}
		}
	}
}

// GetMany fills caller-owned buffers in input order, without snapshot
// semantics. The batch itself allocates nothing.
func (m *ConcurrentMap[K, V]) GetMany(keys []K, values []V, found []bool) {
	if len(values) < len(keys) || len(found) < len(keys) {
		panic("bbtree: short GetMany output")
	}
	root := m.root.Load()
	if !root.batchable() {
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
		descendLanes(root, &probes, &nodes, count, nil)
		if probes[0].numeric && fastDescent {
			_, raw, _ := numericBits(keys[base])
			sign := probes[0].bits ^ raw
			for i := 0; i < count; i++ {
				n := readLeaf(nodes[i], keys[base+i])
				var value V
				ok := false
				if n != nil {
					if e := n.numericEntry(probes[i].bits, probes[i].bits^sign); e != nil {
						value, ok = e.value, true
					}
					n.leaf.mu.RUnlock()
				}
				values[base+i], found[base+i] = value, ok
			}
			continue
		}
		var candidates [lanes]K
		var skips [lanes]int
		var unique [lanes]bool
		for i := 0; i < count; i++ {
			n := readLeaf(nodes[i], keys[base+i])
			var value V
			ok := false
			if n != nil {
				lo, hi := n.bounds(probes[i])
				if hi-lo == 1 {
					e := &n.leaf.entries[lo]
					candidates[i], value = e.key, e.value
					skips[i], unique[i] = n.prefix+4, true
				} else {
					value, ok = n.candidates(probes[i].key, lo, hi)
				}
				n.leaf.mu.RUnlock()
			}
			values[base+i], found[base+i] = value, ok
		}
		if texts, ok := any(&candidates).(*[lanes]string); ok {
			for i := 0; i < count; i++ {
				if skips[i] < len(texts[i]) {
					prefetchText(texts[i], skips[i])
				}
			}
		}
		// Compare immutable key copies after releasing every leaf lock. Each
		// key/value pair was captured together, so the result still describes
		// one instant during this batch. Never hold two leaf locks at once.
		for i := 0; i < count; i++ {
			if unique[i] {
				found[base+i] = windowEqual(probes[i].key, candidates[i], skips[i])
				if !found[base+i] {
					var zero V
					values[base+i] = zero
				}
			}
		}
	}
}
