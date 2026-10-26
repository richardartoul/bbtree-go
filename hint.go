package bbtree

// Hint caches a leaf for GetHint and SetHint. Its zero value is ready to use.
// Hints can help repeated or clustered operations. Benchmark your workload:
// hints can be slower than ordinary Get and Set. A hint may be reused with
// different maps of the same types.
//
// A hint is not safe for concurrent use, even with ConcurrentMap: use one per
// goroutine or synchronize access to it. Hints retain nodes, including nodes
// removed by Clear or Compact, until reused or Reset. Reset hints when finished
// with them if their lifetime exceeds the map's.
type Hint[K Key, V any] struct {
	root *node[K, V]
	leaf *node[K, V]
	low  K
}

// Reset forgets the cached leaf and releases the hint's references.
func (h *Hint[K, V]) Reset() { *h = Hint[K, V]{} }

// remember requires exclusive ownership of the map or a lock on n's leaf.
// The current minimum is a conservative lower fence: deletion and insertion
// cannot move the leaf's routing interval above it. Splits can lower the upper
// fence, so every hinted operation checks the live high fence, not a cached one.
func (h *Hint[K, V]) remember(root, n *node[K, V]) {
	h.root, h.leaf = root, n
	var zero K
	h.low = zero
	if n == nil || n.count == 0 {
		h.leaf = nil
		return
	}
	h.low = n.entries()[0].key
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
		hint.remember(m.root, n)
		if n != nil {
			return n.candidates(key, lo, hi)
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
func (m *Map[K, V]) SetHint(key K, value V, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Set(key, value)
	}
	if m.root == nil {
		old, replaced := m.Set(key, value)
		hint.remember(m.root, m.root)
		return old, replaced
	}
	p := probeOf(key)
	n := hint.leaf
	// Identity with the current rightmost leaf proves ownership even if the
	// root changed. Appending preserves the hint's conservative lower bound.
	if n != nil && n == m.last && n.count > 0 && n.count < capacity && key > n.entries()[n.count-1].key {
		n.put(n.count, p, value)
		m.len++
		hint.root = m.root
		var zero V
		return zero, false
	}
	var lo, hi int
	if hint.root == m.root && n != nil && key >= hint.low && (!n.leaf.hasHigh || key < n.leaf.high) {
		lo, hi = n.bounds(p)
	} else {
		n, lo, hi = m.seek(p)
	}
	i := n.narrow(key, lo, hi)
	if i < hi && n.entries()[i].key == key {
		old := n.entries()[i].value
		n.entries()[i].value = value
		hint.remember(m.root, n)
		return old, true
	}
	if n.count < capacity {
		n.put(i, p, value)
		m.len++
		hint.remember(m.root, n)
		var zero V
		return zero, false
	}
	old, replaced := m.setSplit(p, value)
	hint.remember(m.root, descend(m.root, p))
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
	if hint.root == root && n != nil && key >= hint.low {
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
		hint.remember(root, n)
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
func (m *ConcurrentMap[K, V]) SetHint(key K, value V, hint *Hint[K, V]) (V, bool) {
	if hint == nil {
		return m.Set(key, value)
	}
	p := probeOf(key)
	root := m.root.Load()
	n := hint.leaf
	if hint.root == root && n != nil && key >= hint.low {
		leafOf(n).mu.Lock()
		if n.leaf.hasHigh && key >= n.leaf.high {
			n.leaf.mu.Unlock()
			n = nil
		}
	} else {
		n = nil
	}
	if n == nil {
		n = writeLeaf(descend(root, p), key)
	}
	if n != nil {
		lo, hi := n.bounds(p)
		i := n.narrow(key, lo, hi)
		if i < hi && n.entries()[i].key == key {
			old := n.entries()[i].value
			n.entries()[i].value = value
			hint.remember(root, n)
			n.leaf.mu.Unlock()
			return old, true
		}
		if n.count < capacity {
			n.put(i, p, value)
			hint.remember(root, n)
			n.leaf.mu.Unlock()
			var zero V
			return zero, false
		}
		n.leaf.mu.Unlock()
	}
	old, replaced := m.setSplit(p, value)
	root = m.root.Load()
	n = readLeaf(descend(root, p), key)
	hint.remember(root, n)
	if n != nil {
		n.leaf.mu.RUnlock()
	}
	return old, replaced
}
