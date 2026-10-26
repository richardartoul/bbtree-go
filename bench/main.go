// Command bench compares identical deterministic workloads with bench/reference.rs.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	bbtree "github.com/richardartoul/bbtree-go"
)

func mix(x uint64) uint64 {
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

type options struct {
	n, queries, threads int
	kind, mode, op      string
}

func main() {
	var o options
	var profile string
	flag.IntVar(&o.n, "n", 1<<20, "entries")
	flag.IntVar(&o.queries, "queries", 1<<20, "queries (multiple of 64)")
	flag.IntVar(&o.threads, "threads", 1, "concurrent workers")
	flag.StringVar(&o.kind, "kind", "uint64", "uint64 or string")
	flag.StringVar(&o.mode, "mode", "single", "single or concurrent")
	flag.StringVar(&o.op, "op", "hit", "insert, hit, miss, batch, update, delete, scan64, mixed")
	flag.StringVar(&profile, "cpuprofile", "", "optional CPU profile")
	flag.Parse()
	if o.n < 64 || o.queries < 64 || o.queries%64 != 0 || o.threads < 1 || (o.mode != "single" && o.mode != "concurrent") || (o.threads != 1 && o.mode != "concurrent") {
		panic("invalid benchmark options")
	}
	if profile != "" {
		f, err := os.Create(profile)
		if err != nil {
			panic(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
		}()
	}
	runtime.GOMAXPROCS(o.threads)
	if o.kind == "uint64" {
		run(o, func(x uint64) uint64 { return x }, ^uint64(0))
	} else if o.kind == "string" {
		run(o, func(x uint64) string {
			s := strconv.FormatUint(x, 16)
			return "tenant/region/service/" + strings.Repeat("0", 16-len(s)) + s
		}, "\xff")
	} else {
		panic("unknown key kind")
	}
}

func run[K bbtree.Key](o options, key func(uint64) K, end K) {
	keys := make([]K, o.n)
	for i := range keys {
		keys[i] = key(mix(uint64(2 * i)))
	}
	queries := make([]K, o.queries)
	for i := range queries {
		index := mix(uint64(i)+0x12345678) % uint64(o.n)
		if o.op == "miss" {
			queries[i] = key(mix(2*index + 1))
		} else {
			// Generate independently allocated string probes, not aliases of
			// stored key bytes. The numeric path is identical to copying keys.
			queries[i] = key(mix(2 * index))
		}
	}
	var single bbtree.Map[K, uint64]
	var concurrent bbtree.ConcurrentMap[K, uint64]
	if o.op != "insert" {
		for i, k := range keys {
			if o.mode == "single" {
				single.Set(k, uint64(i)*3+1)
			} else {
				concurrent.Set(k, uint64(i)*3+1)
			}
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var elapsed time.Duration
	var checksum uint64
	ops := o.queries
	if o.op == "insert" || o.op == "delete" {
		ops = o.n
	}
	if o.mode == "single" {
		start := time.Now()
		switch o.op {
		case "insert":
			for i, k := range keys {
				_, replaced := single.Set(k, uint64(i)*3+1)
				if !replaced {
					checksum++
				}
			}
		case "hit", "miss":
			for _, k := range queries {
				v, _ := single.Get(k)
				checksum += v
			}
		case "batch":
			var values [64]uint64
			var found [64]bool
			for i := 0; i < len(queries); i += 64 {
				single.GetMany(queries[i:i+64], values[:], found[:])
				for _, v := range values {
					checksum += v
				}
			}
		case "update":
			for i, k := range queries {
				v, _ := single.Set(k, uint64(i)*5+2)
				checksum += v
			}
		case "delete":
			for _, k := range keys {
				v, _ := single.Delete(k)
				checksum += v
			}
		case "scan64":
			for _, k := range queries {
				count := 0
				for _, v := range single.Range(k, end) {
					checksum += v
					count++
					if count == 64 {
						break
					}
				}
			}
		default:
			panic("unknown single-thread operation")
		}
		elapsed = time.Since(start)
	} else {
		var ready, workers sync.WaitGroup
		start := make(chan struct{})
		sums := make([]uint64, o.threads)
		ready.Add(o.threads)
		for worker := 0; worker < o.threads; worker++ {
			workers.Go(func() {
				ready.Done()
				<-start
				var sum uint64
				begin, finish := ops*worker/o.threads, ops*(worker+1)/o.threads
				switch o.op {
				case "insert":
					for i := begin; i < finish; i++ {
						_, replaced := concurrent.Set(keys[i], uint64(i)*3+1)
						if !replaced {
							sum++
						}
					}
				case "hit", "miss":
					for _, k := range queries[begin:finish] {
						v, _ := concurrent.Get(k)
						sum += v
					}
				case "batch":
					var values [64]uint64
					var found [64]bool
					for i := begin; i < finish; i += 64 {
						j := min(i+64, finish)
						concurrent.GetMany(queries[i:j], values[:j-i], found[:j-i])
						for _, v := range values[:j-i] {
							sum += v
						}
					}
				case "update":
					for i := begin; i < finish; i++ {
						v, _ := concurrent.Set(queries[i], uint64(i)*5+2)
						sum += v
					}
				case "mixed":
					for i := begin; i < finish; i++ {
						if i%10 == 0 {
							concurrent.Set(queries[i], uint64(mix(uint64(i)+0x12345678)%uint64(o.n))*3+1)
						} else {
							v, _ := concurrent.Get(queries[i])
							sum += v
						}
					}
				case "delete":
					for _, k := range keys[begin:finish] {
						v, _ := concurrent.Delete(k)
						sum += v
					}
				case "scan64":
					for _, k := range queries[begin:finish] {
						count := 0
						for _, v := range concurrent.Range(k, end) {
							sum += v
							count++
							if count == 64 {
								break
							}
						}
					}
				default:
					panic("unknown concurrent operation")
				}
				sums[worker] = sum
			})
		}
		ready.Wait()
		begin := time.Now()
		close(start)
		workers.Wait()
		elapsed = time.Since(begin)
		for _, sum := range sums {
			checksum += sum
		}
	}
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(&single)
	runtime.KeepAlive(&concurrent)
	fmt.Printf("go,%s,%s,%s,%d,%d,%d,%.2f,%d,%d\n", o.kind, o.mode, o.op, o.threads, o.n, ops, float64(elapsed.Nanoseconds())/float64(ops), checksum, after.TotalAlloc-before.TotalAlloc)
}
