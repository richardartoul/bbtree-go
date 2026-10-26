**THIS IS JUST A GO TRANSLATION OF THE [ORIGINAL RUST IMPLEMENTATION, XIANGPENGHAO/BBTREE](https://github.com/XiangpengHao/bbtree).**

| Keys | Map | Operation | Workers | Entries | Go ns/op (median of 5) | Rust ns/op (median of 5) | Median paired Go/Rust |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| uint64 | single | insert | 1 | 1,048,576 | 317.2 | 238.0 | 1.33× |
| uint64 | single | hit | 1 | 1,048,576 | 166.2 | 136.4 | 1.22× |
| uint64 | single | miss | 1 | 1,048,576 | 83.7 | 80.3 | 1.05× |
| uint64 | single | batch hit | 1 | 1,048,576 | 71.7 | 86.0 | 0.84× |
| uint64 | single | delete | 1 | 1,048,576 | 326.7 | 291.1 | 1.15× |
| uint64 | single | scan64 | 1 | 1,048,576 | 965.1 | 953.4 | 0.94× |
| uint64 | concurrent | insert | 1 | 1,048,576 | 431.2 | 284.5 | 1.46× |
| uint64 | concurrent | hit | 1 | 1,048,576 | 238.7 | 182.6 | 1.30× |
| uint64 | concurrent | batch hit | 1 | 1,048,576 | 114.3 | 106.1 | 1.08× |
| uint64 | concurrent | insert | 8 | 1,048,576 | 246.9 | 179.7 | 1.38× |
| uint64 | concurrent | hit | 8 | 1,048,576 | 33.9 | 33.6 | 0.97× |
| uint64 | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 39.4 | 37.3 | 0.98× |
| string | single | insert | 1 | 1,048,576 | 730.0 | 549.2 | 1.36× |
| string | single | hit | 1 | 1,048,576 | 642.8 | 638.6 | 1.01× |
| string | single | miss | 1 | 1,048,576 | 300.1 | 287.6 | 1.05× |
| string | single | batch hit | 1 | 1,048,576 | 221.1 | 195.9 | 1.18× |
| string | single | delete | 1 | 1,048,576 | 560.4 | 677.4 | 0.82× |
| string | single | scan64 | 1 | 1,048,576 | 1555.4 | 1787.0 | 0.85× |
| string | concurrent | insert | 1 | 1,048,576 | 917.9 | 639.1 | 1.45× |
| string | concurrent | hit | 1 | 1,048,576 | 772.2 | 688.7 | 1.08× |
| string | concurrent | batch hit | 1 | 1,048,576 | 321.7 | 248.8 | 1.34× |
| string | concurrent | insert | 8 | 1,048,576 | 524.8 | 513.6 | 1.02× |
| string | concurrent | hit | 8 | 1,048,576 | 104.2 | 83.2 | 1.28× |
| string | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 91.0 | 136.3 | 0.65× |
