**THIS IS JUST A GO TRANSLATION OF THE [ORIGINAL RUST IMPLEMENTATION, XIANGPENGHAO/BBTREE](https://github.com/XiangpengHao/bbtree).**

Node searches and numeric descents use AVX-512 (or SSE2) on amd64 and NEON on arm64, including Apple Silicon. Other architectures, and builds with the `purego` tag, use portable Go.

| Keys | Map | Operation | Workers | Entries | Go ns/op (median of 6) | Rust ns/op (median of 6) | Median paired Go/Rust |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| uint64 | single | insert | 1 | 1,048,576 | 211.5 | 185.5 | 1.11× |
| uint64 | single | hit | 1 | 1,048,576 | 98.4 | 98.4 | 1.01× |
| uint64 | single | miss | 1 | 1,048,576 | 56.6 | 61.2 | 0.94× |
| uint64 | single | batch hit | 1 | 1,048,576 | 57.3 | 66.5 | 0.86× |
| uint64 | single | delete | 1 | 1,048,576 | 201.8 | 207.0 | 0.99× |
| uint64 | single | scan64 | 1 | 1,048,576 | 548.4 | 639.2 | 0.89× |
| uint64 | concurrent | insert | 1 | 1,048,576 | 288.1 | 237.0 | 1.23× |
| uint64 | concurrent | hit | 1 | 1,048,576 | 166.7 | 154.3 | 1.09× |
| uint64 | concurrent | batch hit | 1 | 1,048,576 | 98.4 | 84.0 | 1.16× |
| uint64 | concurrent | insert | 8 | 1,048,576 | 226.2 | 174.0 | 1.31× |
| uint64 | concurrent | hit | 8 | 1,048,576 | 23.6 | 21.8 | 1.12× |
| uint64 | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 26.0 | 24.3 | 1.13× |
| string | single | insert | 1 | 1,048,576 | 433.5 | 412.9 | 1.05× |
| string | single | hit | 1 | 1,048,576 | 536.3 | 539.2 | 1.01× |
| string | single | miss | 1 | 1,048,576 | 214.4 | 221.7 | 0.95× |
| string | single | batch hit | 1 | 1,048,576 | 194.8 | 171.4 | 1.14× |
| string | single | delete | 1 | 1,048,576 | 416.5 | 573.5 | 0.74× |
| string | single | scan64 | 1 | 1,048,576 | 1147.0 | 1482.1 | 0.76× |
| string | concurrent | insert | 1 | 1,048,576 | 705.5 | 502.6 | 1.41× |
| string | concurrent | hit | 1 | 1,048,576 | 594.4 | 604.9 | 0.98× |
| string | concurrent | batch hit | 1 | 1,048,576 | 258.0 | 229.2 | 1.12× |
| string | concurrent | insert | 8 | 1,048,576 | 481.5 | 455.7 | 1.04× |
| string | concurrent | hit | 8 | 1,048,576 | 82.8 | 75.2 | 0.94× |
| string | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 78.5 | 118.7 | 0.65× |
