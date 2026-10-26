package bbtree_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"testing"

	bbtree "github.com/richardartoul/bbtree-go"
)

func ExampleHint() {
	var m bbtree.Map[string, int]
	var hint bbtree.Hint[string, int]
	m.SetHint("tenant/001", 10, &hint)
	m.SetHint("tenant/002", 20, &hint)
	value, found := m.GetHint("tenant/001", &hint)
	fmt.Println(value, found)
	hint.Reset()
	// Output: 10 true
}

type hintedOrderedMap[K bbtree.Key] interface {
	orderedMap[K]
	GetHint(K, *bbtree.Hint[K, int64]) (int64, bool)
	SetHint(K, int64, *bbtree.Hint[K, int64]) (int64, bool)
}

type hintedView[K bbtree.Key] struct {
	hintedOrderedMap[K]
	hint *bbtree.Hint[K, int64]
}

func (m hintedView[K]) Get(key K) (int64, bool) { return m.GetHint(key, m.hint) }
func (m hintedView[K]) Set(key K, value int64) (int64, bool) {
	return m.SetHint(key, value, m.hint)
}

func TestHintedMaps(t *testing.T) {
	numeric := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64}
	text := []string{"", "\x00", "a", "a\x00", "a\x00\x00", "\xff"}
	rng := rand.New(rand.NewPCG(38, 7))
	for range 3000 {
		numeric = append(numeric, int64(rng.Uint64()))
		text = append(text, fmt.Sprintf("%s%016x", strings.Repeat("prefix\x00/", 5), rng.Uint64()))
	}
	testHintedMaps(t, numeric)
	testHintedMaps(t, text)
	type namedInt int64
	type namedUint uint64
	type namedText string
	names, unsigned := make([]namedInt, len(numeric)), make([]namedUint, len(numeric))
	for i, k := range numeric {
		names[i], unsigned[i] = namedInt(k), namedUint(k)
	}
	namedStrings := make([]namedText, len(text))
	for i, k := range text {
		namedStrings[i] = namedText(k)
	}
	testHintedMaps(t, names)
	testHintedMaps(t, unsigned)
	testHintedMaps(t, namedStrings)
}

func testHintedMaps[K bbtree.Key](t *testing.T, keys []K) {
	t.Helper()
	for _, concurrent := range []bool{false, true} {
		for _, nilHint := range []bool{false, true} {
			t.Run(fmt.Sprintf("%T/concurrent=%t/nil=%t", keys[0], concurrent, nilHint), func(t *testing.T) {
				var m hintedOrderedMap[K] = new(bbtree.Map[K, int64])
				if concurrent {
					m = new(bbtree.ConcurrentMap[K, int64])
				}
				var hint *bbtree.Hint[K, int64]
				if !nilHint {
					hint = new(bbtree.Hint[K, int64])
				}
				exercise(t, hintedView[K]{m, hint}, keys)
			})
		}
	}
}

func TestHintInvalidation(t *testing.T) {
	testHintInvalidation(t, func(i int) uint64 { return uint64(i) })
	testHintInvalidation(t, func(i int) int64 { return int64(i) - 16000 })
	testHintInvalidation(t, func(i int) string { return fmt.Sprintf("path/shared\x00/%08d", i) })
}

func testHintInvalidation[K bbtree.Key](t *testing.T, key func(int) K) {
	t.Helper()
	var hint bbtree.Hint[K, int64]
	var a, b bbtree.Map[K, int64]
	var c bbtree.ConcurrentMap[K, int64]
	for _, m := range []hintedOrderedMap[K]{&a, &b, &c} {
		if v, ok := m.GetHint(key(0), &hint); ok || v != 0 {
			t.Fatalf("%T empty: (%d,%v)", m, v, ok)
		}
		for i := 0; i < 4096; i++ {
			m.Set(key(i*8), int64(i))
		}
		m.GetHint(key(100*8), &hint)
		staleWrite := hint
		// These local splits shrink the saved leaf's upper bound without
		// growing the root. A cached upper bound must not authorize access.
		for i := 769; i < 1024; i++ {
			m.Set(key(i), int64(i)+99)
		}
		if v, ok := m.GetHint(key(800), &hint); !ok || v != 899 {
			t.Fatalf("%T stale upper-bound read: (%d,%v)", m, v, ok)
		}
		if v, ok := m.SetHint(key(1000), 777, &staleWrite); !ok || v != 1099 {
			t.Fatalf("%T stale upper-bound write: (%d,%v)", m, v, ok)
		}
		if v, ok := m.Get(key(1000)); !ok || v != 777 {
			t.Fatalf("%T stale write missed destination: (%d,%v)", m, v, ok)
		}
		// Keep the hint untouched while ordinary writes split its leaf and
		// change routing/prefix metadata. Check both sides of the old minimum.
		for i := 0; i < 32768; i++ {
			m.Set(key(i), int64(i)+99)
		}
		for _, i := range []int{800, 1000, 799, 0, 32767, 32768} {
			v, ok := m.GetHint(key(i), &hint)
			want, found := m.Get(key(i))
			if v != want || ok != found {
				t.Fatalf("%T stale GetHint(%d): (%d,%v), want (%d,%v)", m, i, v, ok, want, found)
			}
		}
		m.GetHint(key(0), &hint)
		for i := 0; i < 32768; i++ {
			m.Delete(key(i))
		}
		if v, ok := m.GetHint(key(0), &hint); ok || v != 0 {
			t.Fatalf("%T deleted: (%d,%v)", m, v, ok)
		}
		if v, ok := m.SetHint(key(1), 700, &hint); ok || v != 0 {
			t.Fatalf("%T empty leaf insertion: (%d,%v)", m, v, ok)
		}
		if v, ok := m.SetHint(key(1), 701, &hint); !ok || v != 700 {
			t.Fatalf("%T replacement: (%d,%v)", m, v, ok)
		}
	}
	// Rebuild and discard trees without resetting their outstanding hints.
	a.SetHint(key(1), 17, &hint)
	a.Compact()
	a.Set(key(1), 19)
	if v, ok := a.GetHint(key(1), &hint); !ok || v != 19 {
		t.Fatalf("Compact left a stale read: (%d,%v)", v, ok)
	}
	a.Clear()
	a.Set(key(1), 23)
	if v, ok := a.SetHint(key(1), 29, &hint); !ok || v != 23 {
		t.Fatalf("Clear left a stale write: (%d,%v)", v, ok)
	}
	for _, m := range []hintedOrderedMap[K]{&b, &c, &a} {
		want, found := m.Get(key(1))
		if v, ok := m.GetHint(key(1), &hint); v != want || ok != found {
			t.Fatalf("wrong-map reuse %T: (%d,%v), want (%d,%v)", m, v, ok, want, found)
		}
		runtime.GC()
	}
	hint.Reset()
	if v, ok := a.GetHint(key(1), &hint); !ok || v != 29 {
		t.Fatalf("Reset changed the map: (%d,%v)", v, ok)
	}
	if a.Len() != 1 {
		t.Fatalf("hinted writes changed Len: %d", a.Len())
	}
	a.Clear()
	a.Set(key(100), 1)
	a.GetHint(key(100), &hint)
	a.Delete(key(100))
	a.Set(key(10), 2)
	a.SetHint(key(11), 3, &hint)
	if v, ok := a.GetHint(key(11), &hint); !ok || v != 3 || a.Len() != 2 {
		t.Fatalf("append after deleting cached minimum: (%d,%v), Len=%d", v, ok, a.Len())
	}
}

func TestConcurrentHints(t *testing.T) {
	testConcurrentHints(t, func(i int) uint64 { return uint64(i) * 0x9e3779b97f4a7c15 })
	testConcurrentHints(t, func(i int) string { return fmt.Sprintf("tenant/shared/prefix/%016x\x00", uint64(i)*0x9e3779b97f4a7c15) })
}

func testConcurrentHints[K bbtree.Key](t *testing.T, key func(int) K) {
	t.Helper()
	const workers, count = 4, 4000
	type value struct{ id, sequence, check uint64 }
	makeValue := func(id, seq int) value { return value{uint64(id), uint64(seq), uint64(id) ^ uint64(seq) ^ 0x12345678} }
	var m bbtree.ConcurrentMap[K, value]
	m.Set(key(0), makeValue(0, 0))
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(workers)
	for worker := range workers {
		done.Go(func() {
			var read, write bbtree.Hint[K, value]
			m.GetHint(key(0), &read)
			m.GetHint(key(0), &write)
			ready.Done()
			<-start
			for i := 0; i < count; i++ {
				id := i*workers + worker + 1
				k, first, second := key(id), makeValue(id, 1), makeValue(id, 2)
				if old, ok := m.SetHint(k, first, &write); ok || old != (value{}) {
					t.Errorf("insert %d: (%v,%v)", id, old, ok)
					return
				}
				if old, ok := m.SetHint(k, second, &write); !ok || old != first {
					t.Errorf("replace %d: (%v,%v), want %v", id, old, ok, first)
					return
				}
				if got, ok := m.GetHint(k, &read); !ok || got != second {
					t.Errorf("read %d: (%v,%v), want %v", id, got, ok, second)
					return
				}
				if i%7 == 0 {
					m.Delete(k)
					if got, ok := m.GetHint(k, &read); ok || got != (value{}) {
						t.Errorf("deleted %d: (%v,%v)", id, got, ok)
						return
					}
					m.SetHint(k, second, &write)
				}
				neighbor := i*workers + (worker+1)%workers + 1
				if got, ok := m.GetHint(key(neighbor), &read); ok &&
					(got.id != uint64(neighbor) || got.sequence < 1 || got.sequence > 2 || got.check != got.id^got.sequence^0x12345678) {
					t.Errorf("torn concurrent value for %d: %v", neighbor, got)
					return
				}
				if got, ok := m.GetHint(key(0), &read); !ok || got != makeValue(0, 0) {
					t.Errorf("stable key lost: (%v,%v)", got, ok)
					return
				}
			}
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	for id := 1; id <= workers*count; id++ {
		if got, ok := m.Get(key(id)); !ok || got != makeValue(id, 2) {
			t.Fatalf("final key %d: (%v,%v)", id, got, ok)
		}
	}
	// Every replaced token must occur once among the returned values and the
	// final value, even when writers all contend on the same hinted leaf.
	var tokens bbtree.ConcurrentMap[uint64, uint64]
	tokens.Set(0, 0)
	old := make(chan uint64, workers*count)
	for worker := range workers {
		done.Go(func() {
			var hint bbtree.Hint[uint64, uint64]
			for i := 0; i < count; i++ {
				v, ok := tokens.SetHint(0, uint64(worker*count+i+1), &hint)
				if !ok {
					t.Error("contended overwrite inserted a duplicate")
					return
				}
				old <- v
			}
		})
	}
	done.Wait()
	close(old)
	seen := make(map[uint64]bool)
	for v := range old {
		if v > workers*count || seen[v] {
			t.Fatalf("replacement returned invalid or duplicate token %d", v)
		}
		seen[v] = true
	}
	last, ok := tokens.Get(0)
	if !ok || last > workers*count || seen[last] || len(seen) != workers*count {
		t.Fatalf("lost replacement token: last %d, count %d", last, len(seen))
	}
}
