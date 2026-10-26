package bbtree_test

import (
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"

	bbtree "github.com/richardartoul/bbtree-go"
)

// Behavior ports from XiangpengHao/bbtree at
// 8adae97a0ed97e5c98fa2790312eb0aec9719846: unsync/map/tests/prefix.rs,
// differential.rs, map/tests.rs, map/shuttle_tests.rs, and the map/sync_map
// differential fuzz targets. These use only the Go public API. Goroutine
// stress and the race detector do not replace Shuttle's controlled schedules.

func TestPrefixQueriesAndRangeBounds(t *testing.T) {
	prefix := strings.Repeat("path\x00prefix/", 8)
	keys := make([]string, 2048)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s%06d", prefix, i)
	}
	queries := []string{"", "\xff", prefix + "000017", prefix + "000017\x00"}
	for i := 0; i <= len(prefix); i++ {
		queries = append(queries, prefix[:i])
	}
	for _, i := range []int{0, 7, 8, 15, 16, 31, 32, 33, 47, 48, 63, 64, 65, len(prefix) - 1} {
		for _, b := range []byte{'!', '~'} {
			q := []byte(prefix)
			q[i] = b
			queries = append(queries, string(q))
		}
	}
	for _, m := range []orderedMap[string]{new(bbtree.Map[string, int64]), new(bbtree.ConcurrentMap[string, int64])} {
		t.Run(fmt.Sprintf("%T", m), func(t *testing.T) {
			want := make(map[string]int64)
			for i, k := range keys {
				m.Set(k, int64(i)*7+1)
				want[k] = int64(i)*7 + 1
			}
			check := func() {
				checkParity(t, m, want, append(slices.Clone(keys), queries...))
				ordered := slices.Sorted(maps.Keys(want))
				for i, start := range queries {
					end := queries[(i*37+11)%len(queries)]
					checkRange(t, m, want, ordered, start, end, -1)
					checkRange(t, m, want, ordered, start, "\xff", 4)
					checkRange(t, m, want, ordered, start, start, -1)
				}
			}
			check()
			// Remove likely routing separators without inspecting the tree.
			for i := 0; i < len(keys); i += 3 {
				m.Delete(keys[i])
				delete(want, keys[i])
			}
			for i, k := range queries {
				m.Set(k, -int64(i))
				want[k] = -int64(i)
			}
			check()
			for k := range want {
				m.Delete(k)
			}
			clear(want)
			// Reuse emptied leaves with zero-padded windows and new prefixes.
			for i := 0; i < 128; i++ {
				k := "a" + strings.Repeat("\x00", i)
				m.Set(k, int64(i))
				want[k] = int64(i)
			}
			check()
		})
	}
}

func checkRange[K bbtree.Key](t *testing.T, m orderedMap[K], want map[K]int64, ordered []K, start, end K, limit int) {
	t.Helper()
	var expected, got []K
	for _, k := range ordered {
		if k >= start && k < end && (limit < 0 || len(expected) < limit) {
			expected = append(expected, k)
		}
	}
	for k, v := range m.Range(start, end) {
		w, exists := want[k]
		if !exists || v != w {
			t.Fatalf("range returned unexpected %v=%d", k, v)
		}
		got = append(got, k)
		if limit >= 0 && len(got) == limit {
			break
		}
	}
	if !slices.Equal(got, expected) {
		t.Fatalf("range [%v,%v), limit %d: got %v, want %v", start, end, limit, got, expected)
	}
}

func checkParity[K bbtree.Key](t *testing.T, m orderedMap[K], want map[K]int64, queries []K) {
	t.Helper()
	values, found := make([]int64, len(queries)), make([]bool, len(queries))
	for i := range values {
		values[i], found[i] = -999, true
	}
	m.GetMany(queries, values, found)
	for i, k := range queries {
		w, exists := want[k]
		v, ok := m.Get(k)
		if v != w || ok != exists || values[i] != w || found[i] != exists {
			t.Fatalf("key %v: Get (%d,%v), batch (%d,%v), want (%d,%v)", k, v, ok, values[i], found[i], w, exists)
		}
	}
	ordered := slices.Sorted(maps.Keys(want))
	if len(queries) != 0 {
		bounds := slices.Clone(queries)
		slices.Sort(bounds)
		checkRange(t, m, want, ordered, bounds[0], bounds[len(bounds)-1], -1)
	}
	if single, ok := m.(*bbtree.Map[K, int64]); ok {
		if single.Len() != len(want) {
			t.Fatalf("Len = %d, want %d", single.Len(), len(want))
		}
		var got []K
		for k, v := range single.All() {
			w, exists := want[k]
			if !exists || v != w {
				t.Fatalf("All returned unexpected %v=%d", k, v)
			}
			got = append(got, k)
		}
		if !slices.Equal(got, ordered) {
			t.Fatalf("All keys differ: got %v, want %v", got, ordered)
		}
	}
}

func TestLiteralIntegerBounds(t *testing.T) {
	// Range's end is exclusive, even at the largest representable key;
	// unlike Rust's range syntax there is no unbounded ConcurrentMap scan.
	for _, m := range []orderedMap[int64]{new(bbtree.Map[int64, int64]), new(bbtree.ConcurrentMap[int64, int64])} {
		want := map[int64]int64{math.MinInt64: 1, -1: 2, 0: 3, 1: 4, math.MaxInt64: 5}
		for k, v := range want {
			m.Set(k, v)
		}
		bounds := []int64{math.MinInt64, math.MinInt64 + 1, -2, -1, 0, 1, 2, math.MaxInt64 - 1, math.MaxInt64}
		ordered := slices.Sorted(maps.Keys(want))
		for _, start := range bounds {
			for _, end := range bounds {
				checkRange(t, m, want, ordered, start, end, -1)
			}
		}
		checkParity(t, m, want, bounds)
	}
}

// FuzzMapOperations ports the supported subset of upstream's operation-list
// fuzzers, rather than only generating keys and replaying a fixed workload.
// Runs grow trees from small inputs; clustered recipes exercise prefix changes.
func FuzzMapOperations(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 3, 255, 3, 255, 6, 17, 1, 8, 7, 0, 0, 0, 8, 0, 0, 0})
	var runs []byte
	for i := 0; i < 1024; i += 64 {
		runs = append(runs, 3, byte(i>>8), byte(i), 63)
	}
	// Overwrite the grown tree, update an existing entry, compact it, remove
	// alternating runs, then clear and reuse it. This seed keeps the Map's
	// fuzz coverage from depending on a random run without an early Clear.
	runs = append(runs, 0, 0, 17, 93, 5, 0, 17, 3, 7, 0, 0, 0)
	for i := 0; i < 1024; i += 128 {
		runs = append(runs, 4, byte(i>>8), byte(i), 63)
	}
	runs = append(runs, 7, 0, 0, 0, 8, 0, 0, 0, 3, 0, 0, 63)
	f.Add(runs)
	r := rand.New(rand.NewPCG(128, 73))
	seed := make([]byte, 2048)
	for i := range seed {
		seed[i] = byte(r.Uint32())
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		numeric := func(i uint16) uint64 {
			return []uint64{0, 0xabcd000000000000, 1 << 63, 0xfffffffffffff000}[i/256] + uint64(i%256)
		}
		stem := string(data[:min(40, len(data))])
		text := func(i uint16) string {
			prefix := []string{"", "\x00", stem, stem + "/shared/\x00"}[i/256]
			if i%256 == 255 {
				return prefix // A stem alone can prefix other keys.
			}
			return prefix + string([]byte{byte(i % 256), 0})
		}
		fuzzOperations(t, data, numeric)
		fuzzOperations(t, data, func(i uint16) int64 { return int64(numeric(i)) })
		fuzzOperations(t, data, text)
	})
}

func fuzzOperations[K bbtree.Key](t *testing.T, data []byte, key func(uint16) K) {
	t.Helper()
	keys := make([]K, 1024)
	for i := range keys {
		keys[i] = key(uint16(i))
	}
	for _, m := range []orderedMap[K]{new(bbtree.Map[K, int64]), new(bbtree.ConcurrentMap[K, int64])} {
		want := make(map[K]int64)
		for step := 0; step+3 < len(data); step += 4 {
			op, a, b, c := data[step], data[step+1], data[step+2], data[step+3]
			index := (int(a)<<8 | int(b)) % len(keys)
			k, value := keys[index], int64(int8(c))*int64(step+1)
			w, exists := want[k]
			switch op % 9 {
			case 0, 1:
				old, ok := m.Set(k, value)
				if old != w || ok != exists {
					t.Fatalf("step %d Set: (%d,%v), want (%d,%v)", step, old, ok, w, exists)
				}
				want[k] = value
			case 2:
				old, ok := m.Delete(k)
				if old != w || ok != exists {
					t.Fatalf("step %d Delete: (%d,%v), want (%d,%v)", step, old, ok, w, exists)
				}
				delete(want, k)
			case 3, 4:
				for j := 0; j <= int(c)%64; j++ {
					q := keys[(index+j)%len(keys)]
					old, ok := m.Get(q)
					w, exists := want[q]
					if old != w || ok != exists {
						t.Fatalf("step %d run Get(%v): (%d,%v), want (%d,%v)", step, q, old, ok, w, exists)
					}
					if op%9 == 3 {
						old, ok = m.Set(q, value+int64(j))
						want[q] = value + int64(j)
					} else {
						old, ok = m.Delete(q)
						delete(want, q)
					}
					if old != w || ok != exists {
						t.Fatalf("step %d run mutation(%v): (%d,%v), want (%d,%v)", step, q, old, ok, w, exists)
					}
				}
			case 5:
				if concurrent, ok := m.(*bbtree.ConcurrentMap[K, int64]); ok {
					calls := 0
					updated := concurrent.Update(k, func(v int64) int64 { calls++; return v + value })
					if updated != exists || (exists && calls != 1) || (!exists && calls != 0) {
						t.Fatalf("step %d Update: result %v calls %d, presence %v", step, updated, calls, exists)
					}
					if exists {
						want[k] += value
					}
				}
			case 6:
				checkRange(t, m, want, slices.Sorted(maps.Keys(want)), k, keys[int(c)*3], int(b)%9+1)
			case 7:
				if single, ok := m.(*bbtree.Map[K, int64]); ok {
					single.Compact()
				}
			case 8:
				if single, ok := m.(*bbtree.Map[K, int64]); ok {
					single.Clear()
					clear(want)
				}
			}
			if step%64 == 0 {
				checkParity(t, m, want, keys)
			}
		}
		checkParity(t, m, want, keys)
	}
}

func TestConcurrentOwnedKeyModels(t *testing.T) {
	for _, count := range []int{16, 2048} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			concurrentOwnedModels(t, count, func(i int) uint64 { return uint64(i) * 0x9e3779b97f4a7c15 })
			concurrentOwnedModels(t, count, func(i int) string { return fmt.Sprintf("shared/00000000/%06d\x00", i) })
		})
	}
}

func concurrentOwnedModels[K bbtree.Key](t *testing.T, count int, key func(int) K) {
	t.Helper()
	const workers, steps = 4, 8000
	var m bbtree.ConcurrentMap[K, int64]
	models := make([]map[K]int64, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := range workers {
		wg.Go(func() {
			want := make(map[K]int64)
			models[worker] = want
			r := rand.New(rand.NewPCG(uint64(worker)+400, 57))
			<-start
			for step := range steps {
				k := key(r.IntN(count)*workers + worker)
				w, exists := want[k]
				var old int64
				var ok bool
				switch roll := r.IntN(100); {
				case roll < []int{70, 45, 15}[step*3/steps]:
					old, ok = m.Set(k, int64(step))
					want[k] = int64(step)
				case roll < 80:
					old, ok = m.Delete(k)
					delete(want, k)
				case roll < 90:
					ok = m.Update(k, func(v int64) int64 { return v + 3 })
					old = w
					if exists {
						want[k] += 3
					}
				default:
					old, ok = m.Get(k)
				}
				if old != w || ok != exists {
					t.Errorf("%T worker %d step %d key %v: (%d,%v), want (%d,%v)", k, worker, step, k, old, ok, w, exists)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	want := make(map[K]int64)
	for _, model := range models {
		maps.Copy(want, model)
	}
	keys := make([]K, workers*count)
	for i := range keys {
		keys[i] = key(i)
	}
	checkParity(t, &m, want, keys)
}

func TestConcurrentContendedReturns(t *testing.T) {
	// Every inserted token must be returned by exactly one later Set/Delete,
	// or remain in the map. This catches double removals and lost replacements.
	const workers, count = 4, 2048
	var m bbtree.ConcurrentMap[string, int64]
	returned := make([][]int64, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := range workers {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(worker)+300, 91))
			<-start
			for _, i := range r.Perm(count) {
				k := fmt.Sprintf("item/%06d", i)
				if old, ok := m.Set(k, int64(worker*count+i+1)); ok {
					returned[worker] = append(returned[worker], old)
				} else if old != 0 {
					t.Errorf("missing Set returned %d", old)
				}
				if old, ok := m.Delete(k); ok {
					returned[worker] = append(returned[worker], old)
				} else if old != 0 {
					t.Errorf("missing Delete returned %d", old)
				}
			}
		})
	}
	close(start)
	wg.Wait()
	var got []int64
	for _, values := range returned {
		got = append(got, values...)
	}
	for _, v := range m.Range("", "\xff") {
		got = append(got, v)
	}
	slices.Sort(got)
	if len(got) != workers*count {
		t.Fatalf("accounted for %d tokens, want %d", len(got), workers*count)
	}
	for i, v := range got {
		if v != int64(i+1) {
			t.Fatalf("token[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestConcurrentValueCopies(t *testing.T) {
	for _, steady := range []int{8, 200000} {
		t.Run(fmt.Sprintf("numeric/%d", steady), func(t *testing.T) {
			concurrentValueCopies(t, steady, func(i int) uint64 { return 1<<48 + uint64(i) })
		})
	}
	for _, steady := range []int{8, 6000} {
		t.Run(fmt.Sprintf("short-string/%d", steady), func(t *testing.T) {
			concurrentValueCopies(t, steady, func(i int) string { return fmt.Sprintf("%06x", i) })
		})
		t.Run(fmt.Sprintf("long-string/%d", steady), func(t *testing.T) {
			concurrentValueCopies(t, steady, func(i int) string { return fmt.Sprintf("shared/00000000/%06x\x00", i) })
		})
	}
}

func concurrentValueCopies[K bbtree.Key](t *testing.T, steady int, key func(int) K) {
	t.Helper()
	// A multiword value exposes torn copies as well as copies of another
	// entry. No assertion assumes a batch is an atomic snapshot.
	type value struct{ ID, Revision, Check uint64 }
	makeValue := func(i int, revision uint64) value {
		return value{uint64(i), revision, ^uint64(i) ^ revision}
	}
	var m bbtree.ConcurrentMap[K, value]
	for i := range steady {
		m.Set(key(i*2), makeValue(i*2, 0))
	}
	changing := max(steady, 300)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := range 4 {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(worker)+900, 57))
			<-start
			if worker < 2 {
				for step := range 4000 {
					i := r.IntN(changing)*2 + 1
					if step%3 == 0 {
						m.Delete(key(i))
					} else {
						m.Set(key(i), makeValue(i, uint64(step+1)))
					}
					i = r.IntN(steady) * 2
					if !m.Update(key(i), func(v value) value { return makeValue(i, v.Revision+1) }) {
						t.Errorf("Update lost steady key %d", i)
						return
					}
				}
				return
			}
			check := func(i int, v value, found bool) bool {
				if !found {
					if i%2 == 0 || v != (value{}) {
						t.Errorf("missing key %d: value %+v, steady %v", i, v, i%2 == 0)
						return false
					}
				} else if v.ID != uint64(i) || v.Check != ^uint64(i)^v.Revision {
					t.Errorf("key %d: wrong or torn value %+v", i, v)
					return false
				}
				return true
			}
			for round := range 500 {
				// 65 crosses the batch lane boundary twice and mixes stable
				// hits, changing entries, duplicates, and poisoned outputs.
				var keys [65]K
				var ids [65]int
				var values [65]value
				var found [65]bool
				for lane := range keys {
					i := r.IntN(steady) * 2
					if lane%2 == 1 {
						i = r.IntN(changing)*2 + 1
					}
					ids[lane], keys[lane] = i, key(i)
					values[lane], found[lane] = value{99, 99, 99}, true
				}
				keys[64], ids[64] = keys[0], ids[0]
				m.GetMany(keys[:], values[:], found[:])
				for lane, i := range ids {
					if !check(i, values[lane], found[lane]) {
						return
					}
					v, ok := m.Get(keys[lane])
					if !check(i, v, ok) {
						return
					}
				}
				if round%50 == 0 {
					first, previous, evens := true, 0, 0
					for k, v := range m.Range(key(0), key(steady*2)) {
						i := int(v.ID)
						if i < 0 || i >= steady*2 || k != key(i) || (!first && i <= previous) || !check(i, v, true) {
							t.Errorf("invalid scan entry %v=%+v after %d", k, v, previous)
							return
						}
						first, previous = false, i
						if i%2 == 0 {
							evens++
						}
					}
					if evens != steady {
						t.Errorf("scan visited %d steady keys, want %d", evens, steady)
						return
					}
				}
			}
		})
	}
	close(start)
	wg.Wait()
	var updates uint64
	for i := range steady {
		v, ok := m.Get(key(i * 2))
		if !ok || v != makeValue(i*2, v.Revision) {
			t.Fatalf("lost steady value %d: %+v, %v", i*2, v, ok)
		}
		updates += v.Revision
	}
	if updates != 8000 {
		t.Fatalf("retained %d updates, want 8000", updates)
	}
}

func TestZeroSizedAndRetainedValues(t *testing.T) {
	var single bbtree.Map[uint64, struct{}]
	var concurrent bbtree.ConcurrentMap[uint64, struct{}]
	for i := uint64(0); i < 5000; i++ {
		single.Set(i, struct{}{})
		concurrent.Set(i, struct{}{})
	}
	if single.Len() != 5000 {
		t.Fatalf("zero-sized values: Len = %d, want 5000", single.Len())
	}
	for i := uint64(0); i < 5000; i++ {
		if _, ok := single.Get(i); !ok {
			t.Fatalf("zero-sized Map value missing: %d", i)
		}
		if _, ok := concurrent.Get(i); !ok {
			t.Fatalf("zero-sized ConcurrentMap value missing: %d", i)
		}
		if _, ok := single.Delete(i); !ok {
			t.Fatalf("zero-sized Map Delete missed: %d", i)
		}
		if _, ok := concurrent.Delete(i); !ok {
			t.Fatalf("zero-sized ConcurrentMap Delete missed: %d", i)
		}
		if _, ok := single.Get(i); ok {
			t.Fatalf("zero-sized Map value still present: %d", i)
		}
		if _, ok := concurrent.Get(i); ok {
			t.Fatalf("zero-sized ConcurrentMap value still present: %d", i)
		}
	}
	single.Compact()
	if single.Len() != 0 {
		t.Fatal("zero-sized values changed length")
	}
	// Stored values are shallow copies; removing entries and rebuilding the
	// tree must not invalidate values previously handed to callers.
	var m bbtree.Map[string, *[129]uint64]
	v := new([129]uint64)
	v[0], v[128] = 17, 93
	m.Set("retained", v)
	got, ok := m.Get("retained")
	if !ok || got != v {
		t.Fatal("Get did not preserve the stored pointer")
	}
	m.Delete("retained")
	m.Compact()
	m.Clear()
	if got[0] != 17 || got[128] != 93 {
		t.Fatal("retained value changed after deletion/compact/clear")
	}
}

func TestNamedNumericKeys(t *testing.T) {
	type signed int64
	type unsigned uint64
	signedKeys := []signed{math.MinInt64, -1, 0, 1, math.MaxInt64}
	unsignedKeys := []unsigned{0, 1, math.MaxUint64}
	for i := uint64(0); i < 5000; i++ {
		k := i * 0x9e3779b97f4a7c15
		signedKeys = append(signedKeys, signed(k))
		unsignedKeys = append(unsignedKeys, unsigned(k))
	}
	exercise(t, new(bbtree.Map[signed, int64]), signedKeys)
	exercise(t, new(bbtree.ConcurrentMap[signed, int64]), signedKeys)
	exercise(t, new(bbtree.Map[unsigned, int64]), unsignedKeys)
	exercise(t, new(bbtree.ConcurrentMap[unsigned, int64]), unsignedKeys)
}

func TestConcurrentPrefixShrinkPublication(t *testing.T) {
	t.Run("uint64", func(t *testing.T) {
		concurrentPrefixPublication(t, func(i uint64) uint64 { return i })
	})
	t.Run("string", func(t *testing.T) {
		concurrentPrefixPublication(t, func(i uint64) string { return fmt.Sprintf("%016x/shared\x00", i) })
	})
}

func concurrentPrefixPublication[K bbtree.Key](t *testing.T, key func(uint64) K) {
	t.Helper()
	const base = uint64(1 << 48)
	const workers, perWorker = 4, 2000
	var m bbtree.ConcurrentMap[K, int64]
	m.Set(key(base), int64(base)^71)
	published := make(chan uint64, 32)
	start := make(chan struct{})
	var writers, readers sync.WaitGroup
	for range 2 {
		readers.Go(func() {
			for i := range published {
				queries := []K{key(i), key(base), key(i)}
				var values [3]int64
				var found [3]bool
				m.GetMany(queries, values[:], found[:])
				for lane, number := range []uint64{i, base, i} {
					if !found[lane] || values[lane] != int64(number)^71 {
						t.Errorf("published key %d: (%d,%v)", number, values[lane], found[lane])
					}
				}
				if v, ok := m.Get(key(i)); !ok || v != int64(i)^71 {
					t.Errorf("published Get(%d): (%d,%v)", i, v, ok)
				}
			}
		})
	}
	for worker := range workers + 1 {
		writers.Go(func() {
			<-start
			for step := range perWorker {
				i := uint64(step)
				if worker < workers {
					i = base + 1 + uint64(step*workers+worker)
				}
				if old, ok := m.Set(key(i), int64(i)^71); ok || old != 0 {
					t.Errorf("new key %d replaced (%d,%v)", i, old, ok)
				}
				published <- i
			}
		})
	}
	close(start)
	writers.Wait()
	close(published)
	readers.Wait()
	count := 0
	for k, v := range m.Range(key(0), key(base+workers*perWorker+1)) {
		i := uint64(count)
		if count >= perWorker {
			i = base + uint64(count-perWorker)
		}
		if k != key(i) || v != int64(i)^71 {
			t.Fatalf("entry %d: %v=%d, want %v=%d", count, k, v, key(i), int64(i)^71)
		}
		count++
	}
	if count != (workers+1)*perWorker+1 {
		t.Fatalf("scan count %d, want %d", count, (workers+1)*perWorker+1)
	}
}
