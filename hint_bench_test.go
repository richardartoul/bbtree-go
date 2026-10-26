package bbtree_test

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"

	bbtree "github.com/richardartoul/bbtree-go"
)

// BenchmarkHints compares the public APIs with one million randomly inserted
// keys. Clustered streams change their 64-key neighborhood every 256 requests;
// repeated streams change their single key at the same frequency. Concurrent
// maps use one worker here to isolate routing savings from lock contention.
// Changing streams alternate random and clustered phases every 1024 requests.
func BenchmarkHints(b *testing.B) {
	b.Run("uint64", func(b *testing.B) { benchmarkHints(b, func(i int) uint64 { return uint64(i) }) })
	b.Run("string", func(b *testing.B) {
		benchmarkHints(b, func(i int) string { return fmt.Sprintf("tenant/region/shared/%016x", i) })
	})
}

func benchmarkHints[K bbtree.Key](b *testing.B, key func(int) K) {
	const entries, queries = 1 << 20, 1 << 18
	keys := make([]K, entries)
	for i := range keys {
		keys[i] = key(i)
	}
	for _, mode := range []string{"single", "concurrent"} {
		b.Run(mode, func(b *testing.B) {
			var m bbtree.Map[K, uint64]
			var c bbtree.ConcurrentMap[K, uint64]
			set := m.Set
			if mode == "concurrent" {
				set = c.Set
			}
			rng := rand.New(rand.NewPCG(81, 13))
			for _, i := range rng.Perm(entries) {
				set(keys[i], uint64(i)+1)
			}
			for _, locality := range []string{"random", "clustered", "repeated", "changing"} {
				b.Run(locality, func(b *testing.B) {
					width := entries
					if locality == "clustered" {
						width = 64
					} else if locality == "repeated" {
						width = 1
					}
					probes, expected := make([]K, queries), make([]uint64, queries)
					rng := rand.New(rand.NewPCG(32, 19))
					base := 0
					for i := range probes {
						if locality == "changing" {
							width = entries
							if i/1024%2 != 0 {
								width = 64
							}
						}
						if i%256 == 0 {
							base = rng.IntN(entries - width + 1)
						}
						index := base + rng.IntN(width)
						probes[i], expected[i] = keys[index], uint64(index)+1
						if s, ok := any(probes[i]).(string); ok {
							// Compare independently allocated probe bytes, not aliases.
							probes[i] = any(strings.Clone(s)).(K)
						}
					}
					for _, operation := range []string{"get", "overwrite"} {
						for _, hinted := range []bool{false, true} {
							b.Run(fmt.Sprintf("%s/hint=%t", operation, hinted), func(b *testing.B) {
								var hint bbtree.Hint[K, uint64]
								get, set := m.Get, m.Set
								if mode == "concurrent" {
									get, set = c.Get, c.Set
								}
								if hinted {
									get = func(k K) (uint64, bool) { return m.GetHint(k, &hint) }
									set = func(k K, v uint64) (uint64, bool) { return m.SetHint(k, v, &hint) }
									if mode == "concurrent" {
										get = func(k K) (uint64, bool) { return c.GetHint(k, &hint) }
										set = func(k K, v uint64) (uint64, bool) { return c.SetHint(k, v, &hint) }
									}
								}
								// Finish construction's GC before measuring allocation-free operations.
								runtime.GC()
								i := 0
								var sum, want uint64
								b.ReportAllocs()
								for b.Loop() {
									var v uint64
									var ok bool
									if operation == "get" {
										v, ok = get(probes[i])
									} else {
										v, ok = set(probes[i], expected[i])
									}
									if !ok {
										b.Fatal("missing key", probes[i])
									}
									sum, want = sum+v, want+expected[i]
									i = (i + 1) & (queries - 1)
								}
								if sum != want {
									b.Fatal("checksum mismatch", sum, want)
								}
							})
						}
					}
				})
			}
		})
	}
}

// BenchmarkHintInsert loads a fresh map per batch. ns/insert includes splits
// and allocations; ns/op and allocs/op describe an entire 65,536-key batch.
func BenchmarkHintInsert(b *testing.B) {
	b.Run("uint64", func(b *testing.B) { benchmarkHintInsert(b, func(i int) uint64 { return uint64(i) }) })
	b.Run("string", func(b *testing.B) {
		benchmarkHintInsert(b, func(i int) string { return fmt.Sprintf("tenant/region/shared/%016x", i) })
	})
}

func benchmarkHintInsert[K bbtree.Key](b *testing.B, key func(int) K) {
	const entries = 1 << 16
	keys := make([]K, entries)
	for i := range keys {
		keys[i] = key(i)
	}
	for _, locality := range []string{"random", "clustered", "sorted"} {
		b.Run(locality, func(b *testing.B) {
			rng := rand.New(rand.NewPCG(77, 3))
			order := rng.Perm(entries)
			if locality == "sorted" {
				for i := range order {
					order[i] = i
				}
			} else if locality == "clustered" {
				// Shuffle tenant-sized blocks, keeping each block sorted.
				for block, index := range rng.Perm(entries / 256) {
					for j := range 256 {
						order[block*256+j] = index*256 + j
					}
				}
			}
			for _, mode := range []string{"single", "concurrent"} {
				for _, hinted := range []bool{false, true} {
					b.Run(fmt.Sprintf("%s/hint=%t", mode, hinted), func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							var m bbtree.Map[K, uint64]
							var c bbtree.ConcurrentMap[K, uint64]
							var hint bbtree.Hint[K, uint64]
							set := m.Set
							if mode == "concurrent" {
								set = c.Set
							}
							if hinted {
								set = func(k K, v uint64) (uint64, bool) { return m.SetHint(k, v, &hint) }
								if mode == "concurrent" {
									set = func(k K, v uint64) (uint64, bool) { return c.SetHint(k, v, &hint) }
								}
							}
							for _, i := range order {
								if old, replaced := set(keys[i], uint64(i)+1); old != 0 || replaced {
									b.Fatal("insert replaced a key", i, old, replaced)
								}
							}
						}
						b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*entries), "ns/insert")
					})
				}
			}
		})
	}
}
