**THIS IS JUST A GO TRANSLATION OF THE [ORIGINAL RUST IMPLEMENTATION, XIANGPENGHAO/BBTREE](https://github.com/XiangpengHao/bbtree).**

| Keys | Map | Operation | Workers | Entries | Go ns/op (median of 6) | Rust ns/op (median of 6) | Median paired Go/Rust |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| uint64 | single | insert | 1 | 1,048,576 | 198.8 | 177.1 | 1.13× |
| uint64 | single | hit | 1 | 1,048,576 | 77.2 | 78.8 | 0.95× |
| uint64 | single | miss | 1 | 1,048,576 | 53.3 | 53.6 | 1.00× |
| uint64 | single | batch hit | 1 | 1,048,576 | 54.1 | 63.5 | 0.82× |
| uint64 | single | delete | 1 | 1,048,576 | 179.5 | 182.5 | 0.98× |
| uint64 | single | scan64 | 1 | 1,048,576 | 483.5 | 580.5 | 0.83× |
| uint64 | concurrent | insert | 1 | 1,048,576 | 254.3 | 203.7 | 1.25× |
| uint64 | concurrent | hit | 1 | 1,048,576 | 127.3 | 119.0 | 1.10× |
| uint64 | concurrent | batch hit | 1 | 1,048,576 | 83.1 | 72.8 | 1.13× |
| uint64 | concurrent | insert | 8 | 1,048,576 | 213.7 | 161.4 | 1.28× |
| uint64 | concurrent | hit | 8 | 1,048,576 | 23.3 | 21.8 | 1.20× |
| uint64 | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 25.6 | 23.1 | 1.20× |
| string | single | insert | 1 | 1,048,576 | 425.0 | 378.8 | 1.12× |
| string | single | hit | 1 | 1,048,576 | 521.0 | 536.3 | 0.99× |
| string | single | miss | 1 | 1,048,576 | 221.6 | 203.8 | 1.07× |
| string | single | batch hit | 1 | 1,048,576 | 205.7 | 172.3 | 1.19× |
| string | single | delete | 1 | 1,048,576 | 382.0 | 536.4 | 0.72× |
| string | single | scan64 | 1 | 1,048,576 | 1033.5 | 1375.3 | 0.76× |
| string | concurrent | insert | 1 | 1,048,576 | 687.6 | 472.8 | 1.45× |
| string | concurrent | hit | 1 | 1,048,576 | 580.4 | 587.6 | 0.99× |
| string | concurrent | batch hit | 1 | 1,048,576 | 274.3 | 232.3 | 1.18× |
| string | concurrent | insert | 8 | 1,048,576 | 470.6 | 445.4 | 1.02× |
| string | concurrent | hit | 8 | 1,048,576 | 74.0 | 72.4 | 1.03× |
| string | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 70.7 | 124.9 | 0.58× |
