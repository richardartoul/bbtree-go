package bbtree_test

import (
	"fmt"
	"iter"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"

	bbtree "github.com/richardartoul/bbtree-go"
)

type orderedMap[K bbtree.Key] interface {
	Get(K) (int64, bool)
	Set(K, int64) (int64, bool)
	Delete(K) (int64, bool)
	GetMany([]K, []int64, []bool)
	Range(K, K) iter.Seq2[K, int64]
}

func exercise[K bbtree.Key](t *testing.T, m orderedMap[K], keys []K) {
	t.Helper()
	want := make(map[K]int64)
	r := rand.New(rand.NewPCG(17, 41))
	for _, i := range r.Perm(len(keys)) {
		k := keys[i]
		old, replaced := m.Set(k, int64(i*7+3))
		expected, exists := want[k]
		if old != expected || replaced != exists {
			t.Fatalf("Set(%v): (%d,%v), want (%d,%v)", k, old, replaced, expected, exists)
		}
		want[k] = int64(i*7 + 3)
	}
	check := func() {
		t.Helper()
		if len(keys) == 0 {
			return
		}
		for _, k := range keys {
			v, ok := m.Get(k)
			w, found := want[k]
			if v != w || ok != found {
				t.Fatalf("Get(%v): (%d,%v), want (%d,%v)", k, v, ok, w, found)
			}
		}
		ordered := slices.Sorted(maps.Keys(want))
		bounds := slices.Clone(keys)
		slices.Sort(bounds)
		start, end := bounds[len(bounds)/7], bounds[len(bounds)*6/7]
		var got []K
		for k, v := range m.Range(start, end) {
			if v != want[k] {
				t.Fatalf("range value at %v = %d, want %d", k, v, want[k])
			}
			got = append(got, k)
		}
		var expected []K
		for _, k := range ordered {
			if k >= start && k < end {
				expected = append(expected, k)
			}
		}
		if !slices.Equal(got, expected) {
			t.Fatalf("range differs: got %d keys, want %d", len(got), len(expected))
		}
		count := 0
		for range m.Range(start, end) {
			count++
			break
		}
		if count != min(1, len(expected)) {
			t.Fatal("early termination differs")
		}
		for _, count := range []int{0, 1, 7, 8, 9, 31, 32, 33, 65} {
			queries := make([]K, count)
			for i := range queries {
				queries[i] = keys[r.IntN(len(keys))]
			}
			values, found := make([]int64, count), make([]bool, count)
			for i := range values {
				values[i], found[i] = -99, true
			}
			m.GetMany(queries, values, found)
			for i, k := range queries {
				w, exists := want[k]
				if values[i] != w || found[i] != exists {
					t.Fatalf("batch[%d] key %v: (%d,%v), want (%d,%v)", i, k, values[i], found[i], w, exists)
				}
			}
		}
	}
	check()
	for i := 0; i < len(keys)*3; i++ {
		k := keys[r.IntN(len(keys))]
		w, exists := want[k]
		var old int64
		var ok bool
		if i%3 == 0 {
			old, ok = m.Set(k, int64(-i))
			want[k] = int64(-i)
		} else {
			old, ok = m.Delete(k)
			delete(want, k)
		}
		if old != w || ok != exists {
			t.Fatalf("mutation %d key %v: (%d,%v), want (%d,%v)", i, k, old, ok, w, exists)
		}
	}
	check()
	for _, k := range keys {
		m.Delete(k)
	}
	clear(want)
	check()
	// Reuse empty leaves, changing their prefixes in both directions.
	for i, k := range keys {
		m.Set(k, int64(i))
		want[k] = int64(i)
	}
	check()
}

func TestOrderedMaps(t *testing.T) {
	r := rand.New(rand.NewPCG(67, 91))
	numeric := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64}
	text := []string{"", "\x00", "\x00\x00", "a", "a\x00", "a\x00\x00", "ab", "ab\x00", "\xff", "\xff\xff", "é", "世界"}
	for i := 0; i < 12000; i++ {
		numeric = append(numeric, int64(r.Uint64()))
		if i%4 == 0 {
			// Many keys share their leading window; other lanes take the
			// ordinary route. Exercise both in the same batch.
			numeric = append(numeric, int64(0xabcd000000000000|r.Uint64()>>32))
		}
		text = append(text, fmt.Sprintf("tenant/long/common/prefix/%016x/%04d", r.Uint64(), i))
	}
	text = append(text, strings.Repeat("x", 70000), strings.Repeat("x", 70000)+"\x00")
	t.Run("int64", func(t *testing.T) { exercise(t, new(bbtree.Map[int64, int64]), numeric) })
	t.Run("concurrent-int64", func(t *testing.T) { exercise(t, new(bbtree.ConcurrentMap[int64, int64]), numeric) })
	t.Run("string", func(t *testing.T) { exercise(t, new(bbtree.Map[string, int64]), text) })
	t.Run("concurrent-string", func(t *testing.T) { exercise(t, new(bbtree.ConcurrentMap[string, int64]), text) })
	t.Run("named", func(t *testing.T) {
		type key string
		keys := make([]key, len(text))
		for i, k := range text {
			keys[i] = key(k)
		}
		exercise(t, new(bbtree.Map[key, int64]), keys)
	})
}

func TestStringBatchWindowTies(t *testing.T) {
	for _, stored := range [][]string{
		{"a\x00", "b\x00"},
		{"shared/0000/abcd", "shared/9999/wxyz"},
	} {
		want := make(map[string]int64)
		maps := []orderedMap[string]{new(bbtree.Map[string, int64]), new(bbtree.ConcurrentMap[string, int64])}
		for i, k := range stored {
			for _, m := range maps {
				m.Set(k, int64(i)+1)
			}
			want[k] = int64(i) + 1
		}
		queries := append(slices.Clone(stored), "a", "a\x00\x00", "shared/0000/abce", "shared/0000/abcd\x00")
		values, found := make([]int64, len(queries)), make([]bool, len(queries))
		for _, m := range maps {
			m.GetMany(queries, values, found)
			for i, q := range queries {
				value, exists := want[q]
				if values[i] != value || found[i] != exists {
					t.Fatalf("%T batch key %q: (%d,%v), want (%d,%v)", m, q, values[i], found[i], value, exists)
				}
			}
		}
	}
}

func TestCompactAndClear(t *testing.T) {
	var m bbtree.Map[uint64, string]
	for i := uint64(0); i < 10000; i++ {
		m.Set(i, fmt.Sprint(i))
	}
	for i := uint64(0); i < 10000; i++ {
		if i%71 != 0 {
			m.Delete(i)
		}
	}
	before := make(map[uint64]string)
	for k, v := range m.All() {
		before[k] = v
	}
	m.Compact()
	if m.Len() != len(before) {
		t.Fatalf("length = %d, want %d", m.Len(), len(before))
	}
	for k, v := range m.All() {
		if w, ok := before[k]; !ok || v != w {
			t.Fatalf("unexpected entry %d=%s", k, v)
		}
		delete(before, k)
	}
	if len(before) != 0 {
		t.Fatal("compact lost entries")
	}
	m.Clear()
	if m.Len() != 0 {
		t.Fatal("clear left entries")
	}
	m.GetMany([]uint64{0, math.MaxUint64}, make([]string, 2), make([]bool, 2))
	for range m.All() {
		t.Fatal("clear left iterator entries")
	}
	for _, size := range []int{0, 1, 64, 65, 4161, 10000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var m bbtree.Map[uint64, uint64]
			for i := 0; i < size; i++ {
				m.Set(uint64(i)+10, uint64(i)*9+1)
			}
			m.Compact()
			for i := 0; i < size; i++ {
				if v, ok := m.Get(uint64(i) + 10); !ok || v != uint64(i)*9+1 {
					t.Fatalf("compact lost key %d", i+10)
				}
			}
			m.Set(0, 77)
			m.Set(math.MaxUint64, 99)
			queries := []uint64{0, 9, 10, uint64(size) + 10, math.MaxUint64}
			values, found := make([]uint64, len(queries)), make([]bool, len(queries))
			m.GetMany(queries, values, found)
			if values[0] != 77 || !found[0] || found[1] || found[3] || values[4] != 99 || !found[4] {
				t.Fatalf("mutations after compact: %v, %v", values, found)
			}
			if m.Len() != size+2 {
				t.Fatalf("length after compact and insert: %d", m.Len())
			}
		})
	}
}

func TestReadsDoNotAllocate(t *testing.T) {
	var single bbtree.Map[uint64, int64]
	var concurrent bbtree.ConcurrentMap[uint64, int64]
	for i := uint64(0); i < 10000; i++ {
		single.Set(i, int64(i)*7)
		concurrent.Set(i, int64(i)*7)
	}
	keys := []uint64{9919, 1, 7000, 10000, 17, 191, 36, 0, math.MaxUint64}
	values, found := make([]int64, len(keys)), make([]bool, len(keys))
	allocs := testing.AllocsPerRun(100, func() {
		single.Get(9919)
		concurrent.Get(9919)
		single.GetMany(keys, values, found)
		concurrent.GetMany(keys, values, found)
	})
	if allocs != 0 {
		t.Fatalf("reads allocated: %g", allocs)
	}
}

func TestBatchesAcrossDepths(t *testing.T) {
	t.Run("uint64", func(t *testing.T) {
		checkGrowingBatches(t, func(i uint64) uint64 { return i * 0x9e3779b97f4a7c15 })
	})
	t.Run("int64", func(t *testing.T) {
		checkGrowingBatches(t, func(i uint64) int64 { return int64(i * 0x9e3779b97f4a7c15) })
	})
	t.Run("string", func(t *testing.T) {
		checkGrowingBatches(t, func(i uint64) string { return fmt.Sprintf("shared/%016x\x00", i*0x9e3779b97f4a7c15) })
	})
}

func checkGrowingBatches[K bbtree.Key](t *testing.T, key func(uint64) K) {
	t.Helper()
	for _, m := range []orderedMap[K]{new(bbtree.Map[K, int64]), new(bbtree.ConcurrentMap[K, int64])} {
		inserted := 0
		for _, size := range []int{0, 80, 200000} {
			for ; inserted < size; inserted++ {
				m.Set(key(uint64(2*inserted)), int64(inserted)*7+3)
			}
			for _, count := range []int{1, 7, 8, 31, 32, 33, 65, 131} {
				queries := make([]K, count)
				values, found := make([]int64, count), make([]bool, count)
				indices := make([]uint64, count)
				for i := range queries {
					// Independently permuted hits, missing keys, and duplicates.
					indices[i] = (uint64(i%97)*7919 + 17) % uint64(max(2, size*3))
					queries[i], values[i], found[i] = key(indices[i]), -99, true
				}
				m.GetMany(queries, values, found)
				for i, index := range indices {
					exists := index%2 == 0 && index < uint64(2*size)
					var want int64
					if exists {
						want = int64(index/2)*7 + 3
					}
					if found[i] != exists || values[i] != want {
						t.Fatalf("%T size %d batch[%d]: (%d,%v), want (%d,%v)", m, size, i, values[i], found[i], want, exists)
					}
				}
				if count == 33 {
					if allocs := testing.AllocsPerRun(10, func() { m.GetMany(queries, values, found) }); allocs != 0 {
						t.Fatalf("%T size %d batch allocated: %g", m, size, allocs)
					}
				}
			}
		}
	}
}

func TestConcurrentSplitsAndUpdates(t *testing.T) {
	var m bbtree.ConcurrentMap[uint64, uint64]
	const workers, perWorker = 8, 3000
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			<-start
			for i := 0; i < perWorker; i++ {
				k := uint64(i*workers + worker)
				m.Set(k, k*13+7)
				if v, ok := m.Get(k); !ok || v != k*13+7 {
					t.Errorf("lost insert %d", k)
				}
				if i%2 == 0 {
					m.Delete(k)
					m.Set(k, k*13+7)
				}
				if i%32 == 31 {
					var keys, values [32]uint64
					var found [32]bool
					for j := range keys {
						keys[j] = uint64((i-j)*workers + worker)
					}
					m.GetMany(keys[:], values[:], found[:])
					for j, key := range keys {
						if !found[j] || values[j] != key*13+7 {
							t.Errorf("batch lost insert %d: (%d,%v)", key, values[j], found[j])
							return
						}
					}
				}
			}
		})
	}
	close(start)
	wg.Wait()
	count := 0
	for k, v := range m.Range(0, workers*perWorker) {
		if k != uint64(count) || v != k*13+7 {
			t.Fatalf("scan entry %d: %d=%d", count, k, v)
		}
		count++
	}
	if count != workers*perWorker {
		t.Fatalf("scan count %d", count)
	}
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			for i := 0; i < 2000; i++ {
				m.Update(19, func(v uint64) uint64 { return v + 1 })
			}
		})
	}
	wg.Wait()
	if got, _ := m.Get(19); got != 19*13+7+workers*2000 {
		t.Fatalf("lost atomic updates: %d", got)
	}
}

func TestConcurrentStringSplits(t *testing.T) {
	var m bbtree.ConcurrentMap[string, uint64]
	const workers, perWorker = 4, 2000
	key := func(i int) string {
		x := uint64(i) * 0x9e3779b97f4a7c15
		return fmt.Sprintf("tenant/%016x", x^(x>>29))
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			var queries [32]string
			var values [32]uint64
			var found [32]bool
			for i := 0; i < perWorker; i++ {
				index := i*workers + worker
				m.Set(key(index), uint64(index)+1)
				count := min(i+1, len(queries))
				for j := 0; j < count; j++ {
					queries[j] = key((i-j)*workers + worker)
				}
				m.GetMany(queries[:count], values[:count], found[:count])
				for j := 0; j < count; j++ {
					want := uint64((i-j)*workers+worker) + 1
					if !found[j] || values[j] != want {
						t.Errorf("lost string key %q: (%d,%v), want %d", queries[j], values[j], found[j], want)
						return
					}
				}
			}
		})
	}
	wg.Wait()
	for i := 0; i < workers*perWorker; i++ {
		if v, ok := m.Get(key(i)); !ok || v != uint64(i)+1 {
			t.Fatalf("lost string key %d after splits: (%d,%v)", i, v, ok)
		}
	}
}

func TestConcurrentScanDuringSplits(t *testing.T) {
	var m bbtree.ConcurrentMap[uint64, uint64]
	const limit = 10000
	for k := uint64(0); k < limit; k += 2 {
		m.Set(k, k+5)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for k := uint64(1); k < limit; k += 2 {
			m.Set(k, k+5)
			m.Delete(k)
			m.Set(k, k+5)
		}
	})
	for round := 0; round < 20; round++ {
		even, prev := 0, uint64(0)
		first := true
		for k, v := range m.Range(0, limit) {
			if !first && k <= prev || v != k+5 {
				t.Fatalf("unordered or invalid scan: %d=%d after %d", k, v, prev)
			}
			first, prev = false, k
			if k%2 == 0 {
				even++
			}
		}
		if even != limit/2 {
			t.Fatalf("continuously present keys: %d, want %d", even, limit/2)
		}
	}
	wg.Wait()
}

func TestCallbackPanicReleasesLocks(t *testing.T) {
	var m bbtree.ConcurrentMap[string, int]
	m.Set("a", 7)
	func() {
		defer func() { _ = recover() }()
		m.Update("a", func(int) int { panic("expected") })
	}()
	if v, ok := m.Get("a"); !ok || v != 7 {
		t.Fatal("panic changed value")
	}
	func() {
		defer func() { _ = recover() }()
		m.Range("", "z")(func(string, int) bool { panic("expected") })
	}()
	m.Set("a", 9)
	if v, _ := m.Get("a"); v != 9 {
		t.Fatal("map unusable after callback panic")
	}
}

func FuzzStringMap(f *testing.F) {
	f.Add([]byte("\x00a\x00\x00ab\xff\xffprefix/common/path/"))
	f.Add([]byte(strings.Repeat("shared-prefix\x00", 100)))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		keys := []string{"", "\x00", "\xff\xff"}
		for i := 0; i < len(data); i++ {
			end := min(len(data), i+1+int(data[i])%31)
			keys = append(keys, string(data[i:end]))
		}
		exercise(t, new(bbtree.Map[string, int64]), keys)
	})
}
