use bbtree::{BBTreeMap, unsync};
use std::{env, hint::black_box, sync::Barrier, thread, time::Instant};

fn mix(mut x: u64) -> u64 {
    x = (x ^ (x >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    x = (x ^ (x >> 27)).wrapping_mul(0x94d049bb133111eb);
    x ^ (x >> 31)
}

struct Options {
    n: usize,
    queries: usize,
    threads: usize,
    kind: String,
    mode: String,
    op: String,
}

fn main() {
    let args: Vec<_> = env::args().collect();
    let arg = |name: &str, default: &str| {
        args.windows(2)
            .find(|a| a[0] == name)
            .map_or_else(|| default.to_owned(), |a| a[1].clone())
    };
    let o = Options {
        n: arg("-n", "1048576").parse().unwrap(),
        queries: arg("-queries", "1048576").parse().unwrap(),
        threads: arg("-threads", "1").parse().unwrap(),
        kind: arg("-kind", "uint64"),
        mode: arg("-mode", "single"),
        op: arg("-op", "hit"),
    };
    assert!(o.n >= 64 && o.queries >= 64 && o.queries % 64 == 0);
    assert!(o.threads >= 1 && (o.mode == "concurrent" || o.threads == 1));
    match o.kind.as_str() {
        "uint64" => run(&o, |x| x, u64::MAX),
        "string" => run(
            &o,
            |x| format!("tenant/region/service/{x:016x}"),
            "\u{7f}".to_owned(),
        ),
        _ => panic!("unknown key kind"),
    }
}

fn run<K>(o: &Options, key: impl Fn(u64) -> K, end: K)
where
    K: Ord + Clone + Send + Sync + 'static,
    bbtree::Auto: bbtree::InlineKeyStrategy<K>,
{
    let keys: Vec<_> = (0..o.n).map(|i| key(mix(2 * i as u64))).collect();
    let queries: Vec<_> = (0..o.queries)
        .map(|i| {
            let index = mix(i as u64 + 0x12345678) % o.n as u64;
            key(mix(2 * index + u64::from(o.op == "miss")))
        })
        .collect();
    let mut single = unsync::BBTreeMap::<K, u64>::new();
    let concurrent = BBTreeMap::<K, u64>::new();
    if o.op != "insert" {
        for (i, k) in keys.iter().enumerate() {
            if o.mode == "single" {
                single.insert(k.clone(), i as u64 * 3 + 1);
            } else {
                concurrent.insert(k.clone(), i as u64 * 3 + 1).unwrap();
            }
        }
    }
    // Transfer prepared keys into inserts where the Go timed loop copies a
    // string descriptor. Rust must own its keys, but need not allocate copies.
    let mut insert_keys = if o.op == "insert" {
        keys.clone()
    } else {
        Vec::new()
    };
    let mut update_keys = if o.op == "update" || o.op == "mixed" {
        queries.clone()
    } else {
        Vec::new()
    };
    let ops = if o.op == "insert" || o.op == "delete" {
        o.n
    } else {
        o.queries
    };
    let mut checksum = 0_u64;
    let elapsed;
    if o.mode == "single" {
        let start = Instant::now();
        match o.op.as_str() {
            "insert" => {
                for (i, k) in insert_keys.drain(..).enumerate() {
                    checksum += u64::from(single.insert(k, i as u64 * 3 + 1).is_none());
                }
            }
            "hit" | "miss" => {
                for k in &queries {
                    checksum =
                        checksum.wrapping_add(single.get(black_box(k)).copied().unwrap_or(0));
                }
            }
            "batch" => {
                for chunk in queries.chunks_exact(64) {
                    let refs: [&K; 64] = std::array::from_fn(|i| &chunk[i]);
                    for value in single.get_many(refs) {
                        checksum = checksum.wrapping_add(value.copied().unwrap_or(0));
                    }
                }
            }
            "update" => {
                for (i, k) in update_keys.drain(..).enumerate() {
                    checksum = checksum.wrapping_add(single.insert(k, i as u64 * 5 + 2).unwrap());
                }
            }
            "delete" => {
                for k in &keys {
                    checksum = checksum.wrapping_add(single.remove(k).unwrap());
                }
            }
            "scan64" => {
                for k in &queries {
                    for (_, v) in single.range(k..&end).take(64) {
                        checksum = checksum.wrapping_add(*v);
                    }
                }
            }
            _ => panic!("unknown single-thread operation"),
        }
        elapsed = start.elapsed();
    } else {
        let barrier = Barrier::new(o.threads + 1);
        // The prepared owned keys are partitioned outside the timed interval.
        let mut owned: Vec<Vec<(usize, K)>> = (0..o.threads).map(|_| Vec::new()).collect();
        let source = if o.op == "insert" {
            &mut insert_keys
        } else {
            &mut update_keys
        };
        let source_len = source.len();
        for (i, k) in source.drain(..).enumerate() {
            let worker = ((i + 1) * o.threads - 1) / source_len;
            owned[worker].push((i, k));
        }
        elapsed = thread::scope(|scope| {
            let mut handles = Vec::new();
            for (worker, entries) in owned.into_iter().enumerate() {
                let (queries, keys, map, barrier, end) =
                    (&queries, &keys, &concurrent, &barrier, &end);
                handles.push(scope.spawn(move || {
                    let begin = ops * worker / o.threads;
                    let finish = ops * (worker + 1) / o.threads;
                    let mut sum = 0_u64;
                    barrier.wait();
                    match o.op.as_str() {
                        "insert" => {
                            for (i, k) in entries {
                                sum +=
                                    u64::from(map.insert(k, i as u64 * 3 + 1).unwrap().is_none());
                            }
                        }
                        "hit" | "miss" => {
                            for k in &queries[begin..finish] {
                                sum = sum.wrapping_add(map.get_copied(black_box(k)).unwrap_or(0));
                            }
                        }
                        "batch" => {
                            assert_eq!((finish - begin) % 64, 0);
                            for chunk in queries[begin..finish].chunks_exact(64) {
                                let refs: [&K; 64] = std::array::from_fn(|i| &chunk[i]);
                                for value in map.get_many_copied(refs) {
                                    sum = sum.wrapping_add(value.unwrap_or(0));
                                }
                            }
                        }
                        "update" => {
                            for (i, k) in entries {
                                sum = sum.wrapping_add(
                                    map.insert(k, i as u64 * 5 + 2).unwrap().unwrap(),
                                );
                            }
                        }
                        "mixed" => {
                            for (i, k) in entries {
                                if i % 10 == 0 {
                                    let index = mix(i as u64 + 0x12345678) % o.n as u64;
                                    map.insert(k, index * 3 + 1).unwrap();
                                } else {
                                    sum = sum.wrapping_add(map.get_copied(&k).unwrap());
                                }
                            }
                        }
                        "delete" => {
                            for k in &keys[begin..finish] {
                                sum = sum.wrapping_add(map.remove(k).unwrap());
                            }
                        }
                        "scan64" => {
                            for k in &queries[begin..finish] {
                                let mut count = 0;
                                map.scan(k..end, |_, v| {
                                    sum = sum.wrapping_add(*v);
                                    count += 1;
                                    if count == 64 {
                                        std::ops::ControlFlow::Break(())
                                    } else {
                                        std::ops::ControlFlow::Continue(())
                                    }
                                });
                            }
                        }
                        _ => panic!("unknown concurrent operation"),
                    }
                    sum
                }));
            }
            let start = Instant::now();
            barrier.wait();
            for h in handles {
                checksum = checksum.wrapping_add(h.join().unwrap());
            }
            start.elapsed()
        });
    }
    black_box((&single, &concurrent));
    println!(
        "rust,{},{},{},{},{},{},{:.2},{},NA",
        o.kind,
        o.mode,
        o.op,
        o.threads,
        o.n,
        ops,
        elapsed.as_nanos() as f64 / ops as f64,
        black_box(checksum)
    );
}
