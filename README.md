**THIS IS JUST A GO TRANSLATION OF THE [ORIGINAL RUST IMPLEMENTATION, XIANGPENGHAO/BBTREE](https://github.com/XiangpengHao/bbtree).**

| Keys | Map | Operation | Workers | Entries | Go ns/op (median of 6) | Rust ns/op (median of 6) | Median paired Go/Rust |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| uint64 | single | insert | 1 | 1,048,576 | 215.2 | 187.5 | 1.13× |
| uint64 | single | hit | 1 | 1,048,576 | 87.4 | 82.4 | 1.04× |
| uint64 | single | miss | 1 | 1,048,576 | 55.4 | 60.4 | 0.97× |
| uint64 | single | batch hit | 1 | 1,048,576 | 55.5 | 64.3 | 0.86× |
| uint64 | single | delete | 1 | 1,048,576 | 199.7 | 180.8 | 1.02× |
| uint64 | single | scan64 | 1 | 1,048,576 | 541.0 | 634.3 | 0.81× |
| uint64 | concurrent | insert | 1 | 1,048,576 | 289.8 | 218.2 | 1.30× |
| uint64 | concurrent | hit | 1 | 1,048,576 | 155.4 | 125.4 | 1.24× |
| uint64 | concurrent | batch hit | 1 | 1,048,576 | 90.3 | 84.7 | 1.06× |
| uint64 | concurrent | insert | 8 | 1,048,576 | 208.8 | 164.8 | 1.28× |
| uint64 | concurrent | hit | 8 | 1,048,576 | 25.5 | 19.7 | 1.33× |
| uint64 | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 31.0 | 27.5 | 1.19× |
| string | single | insert | 1 | 1,048,576 | 453.6 | 394.0 | 1.14× |
| string | single | hit | 1 | 1,048,576 | 559.6 | 527.0 | 1.04× |
| string | single | miss | 1 | 1,048,576 | 250.1 | 211.6 | 1.13× |
| string | single | batch hit | 1 | 1,048,576 | 207.2 | 171.0 | 1.20× |
| string | single | delete | 1 | 1,048,576 | 433.4 | 511.6 | 0.79× |
| string | single | scan64 | 1 | 1,048,576 | 1147.6 | 1440.1 | 0.80× |
| string | concurrent | insert | 1 | 1,048,576 | 671.6 | 458.3 | 1.48× |
| string | concurrent | hit | 1 | 1,048,576 | 581.9 | 549.3 | 1.06× |
| string | concurrent | batch hit | 1 | 1,048,576 | 273.0 | 239.2 | 1.15× |
| string | concurrent | insert | 8 | 1,048,576 | 441.9 | 444.3 | 0.99× |
| string | concurrent | hit | 8 | 1,048,576 | 72.9 | 69.4 | 1.05× |
| string | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 74.7 | 114.5 | 0.66× |
