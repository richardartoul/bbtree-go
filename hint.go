package bbtree

// Hint caches a leaf for GetHint and SetHint. Its zero value is ready to use.
// Hints can help repeated or clustered operations. Benchmark your workload:
// hints can be slower than ordinary Get and Set. A hint may be reused with
// different maps of the same types.
//
// A hint is not safe for concurrent use, even with ConcurrentMap: use one per
// goroutine or synchronize access to it. Hints retain nodes, including nodes
// removed by Clear or Compact, until refreshed or Reset. Reset hints when
// finished with them if their lifetime exceeds the map's.
type Hint[K Key, V any] struct {
	root    *node[K, V]
	leaf    *node[K, V]
	low     K
	high    K
	hasHigh bool
	misses  uint8
	skip    uint8
}

// Reset forgets the cached leaf and releases the hint's references.
func (h *Hint[K, V]) Reset() { *h = Hint[K, V]{} }

// Sixteen consecutive misses indicate little locality. Sample again after 128
// ordinary writes so a random stream does not pay to refresh every hint.
func (h *Hint[K, V]) miss() {
	h.misses++
	if h.misses == 16 {
		h.misses = 0
		h.skip = 128
	}
}

// remember requires exclusive ownership of the map or a lock on n's leaf.
// The anchor is a conservative lower fence: it is a key from this leaf, and
// splits reset it before shrinking the interval. Deletion and insertion can
// make it stale but cannot move it below the leaf's routing interval. The saved
// upper bound only rejects misses without accessing the leaf; accepting a hint
// still requires checking the live high fence.
// Only concurrent numeric paths cache the upper bound: string comparisons are
// too expensive to repeat before and after locking.
func (h *Hint[K, V]) remember(root, n *node[K, V], cacheHigh bool) {
	if n == nil || n.count == 0 {
		*h = Hint[K, V]{root: root}
		return
	}
	h.root, h.leaf = root, n
	h.low = n.anchor
	h.hasHigh = cacheHigh && n.leaf.hasHigh
	if h.hasHigh {
		h.high = n.leaf.high
	}
}

// GetHint is Get with a caller-owned leaf hint. A nil hint is equivalent to Get.
// Splits, Clear, Compact and reuse with another map are handled automatically.
func (m *Map[K, V]) GetHint(key K, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Get(key)
	}
	n := hint.leaf
	if hint.root != m.root || n == nil || key < hint.low || n.leaf.hasHigh && key >= n.leaf.high {
		n, lo, hi := m.seek(probeOf(key))
		hint.remember(m.root, n, false)
		if lo < hi {
			if hi-lo > 1 {
				return n.collision(key, lo, hi)
			}
			if e := &n.entries()[lo]; e.key == key {
				return e.value, true
			}
		}
		var zero V
		return zero, false
	}
	bits, raw, numeric := numericBits(key)
	if numeric && fastDescent {
		if e := n.numericEntry(bits, raw); e != nil {
			return e.value, true
		}
		var zero V
		return zero, false
	}
	return n.lookup(probe[K]{key, bits, numeric})
}

// SetHint is Set with a caller-owned leaf hint. A nil hint is equivalent to Set.
// Splits, Clear, Compact and reuse with another map are handled automatically.
// Sustained cache misses temporarily fall back to ordinary Set.
func (m *Map[K, V]) SetHint(key K, value V, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Set(key, value)
	}
	if hint.skip != 0 {
		hint.skip--
		return m.Set(key, value)
	}
	if m.root == nil {
		old, replaced := m.Set(key, value)
		hint.remember(m.root, m.root, false)
		return old, replaced
	}
	p := probeOf(key)
	n := hint.leaf
	// Avoid touching the unrelated last leaf when a clustered hint is active.
	// An empty hint can still use the map's own rightmost-leaf append cache.
	if n == nil || n == m.last {
		if last := m.last; last.count > 0 && last.count < capacity && key > last.entries()[last.count-1].key {
			last.put(last.count, p, value)
			m.len++
			var zero V
			return zero, false
		}
	}
	var lo, hi int
	cached := hint.root == m.root && n != nil && key >= hint.low && (!n.leaf.hasHigh || key < n.leaf.high)
	if cached {
		hint.misses = 0
		lo, hi = n.bounds(p)
	} else {
		if n != nil {
			hint.miss()
		}
		n, lo, hi = m.seek(p)
	}
	i := n.narrow(key, lo, hi)
	if i < hi && n.entries()[i].key == key {
		old := n.entries()[i].value
		n.entries()[i].value = value
		if !cached {
			hint.remember(m.root, n, false)
		}
		return old, true
	}
	if n.count < capacity {
		n.put(i, p, value)
		m.len++
		if !cached {
			hint.remember(m.root, n, false)
		}
		var zero V
		return zero, false
	}
	old, replaced := m.setSplit(p, value)
	// Let the next operation refresh the hint; sorted appends can use m.last.
	hint.leaf = nil
	return old, replaced
}

// GetHint is Get with a caller-owned leaf hint. A nil hint is equivalent to Get.
// The destination leaf is still locked. Do not share a hint across concurrent
// calls; use a separate hint per goroutine.
func (m *ConcurrentMap[K, V]) GetHint(key K, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Get(key)
	}
	bits, raw, numeric := numericBits(key)
	p := probe[K]{key, bits, numeric}
	root := m.root.Load()
	n := hint.leaf
	if hint.root == root && n != nil && key >= hint.low && (!hint.hasHigh || key < hint.high) {
		n.leaf.mu.RLock()
		if n.leaf.hasHigh && key >= n.leaf.high {
			n.leaf.mu.RUnlock()
			n = nil
		}
	} else {
		n = nil
	}
	if n == nil {
		n = readLeaf(descend(root, p), key)
		hint.remember(root, n, numeric)
	}
	if n != nil {
		var value V
		var ok bool
		if numeric && fastDescent {
			if e := n.numericEntry(bits, raw); e != nil {
				value, ok = e.value, true
			}
		} else {
			value, ok = n.lookup(p)
		}
		n.leaf.mu.RUnlock()
		return value, ok
	}
	var zero V
	return zero, false
}

// SetHint is Set with a caller-owned leaf hint. A nil hint is equivalent to Set.
// The destination leaf is still locked. Do not share a hint across concurrent
// calls; use a separate hint per goroutine.
// Sustained cache misses temporarily fall back to ordinary Set.
func (m *ConcurrentMap[K, V]) SetHint(key K, value V, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Set(key, value)
	}
	if hint.skip != 0 {
		hint.skip--
		return m.Set(key, value)
	}
	p := probeOf(key)
	root := m.root.Load()
	n := hint.leaf
	cached := hint.root == root && n != nil && key >= hint.low && (!hint.hasHigh || key < hint.high)
	if cached {
		leafOf(n).mu.Lock()
		if n.leaf.hasHigh && key >= n.leaf.high {
			n.leaf.mu.Unlock()
			n = nil
			cached = false
		}
	} else {
		n = nil
	}
	if n == nil {
		if hint.leaf != nil {
			hint.miss()
		}
		n = writeLeaf(descend(root, p), key)
	} else {
		hint.misses = 0
	}
	if n != nil {
		lo, hi := n.bounds(p)
		i := n.narrow(key, lo, hi)
		if i < hi && n.entries()[i].key == key {
			old := n.entries()[i].value
			n.entries()[i].value = value
			if !cached {
				hint.remember(root, n, p.numeric)
			}
			n.leaf.mu.Unlock()
			return old, true
		}
		if n.count < capacity {
			n.put(i, p, value)
			if !cached {
				hint.remember(root, n, p.numeric)
			}
			n.leaf.mu.Unlock()
			var zero V
			return zero, false
		}
		n.leaf.mu.Unlock()
	}
	// The next operation validates the old hint under its destination lock.
	// Refreshing here would require another traversal and a separate read lock.
	return m.setSplit(p, value)
}
