**THIS IS JUST A GO TRANSLATION OF THE [ORIGINAL RUST IMPLEMENTATION, XIANGPENGHAO/BBTREE](https://github.com/XiangpengHao/bbtree).**

| Keys | Map | Operation | Workers | Entries | Go ns/op (median of 6) | Rust ns/op (median of 6) | Median paired Go/Rust |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| uint64 | single | insert | 1 | 1,048,576 | 205.8 | 179.8 | 1.17× |
| uint64 | single | hit | 1 | 1,048,576 | 76.3 | 82.4 | 0.98× |
| uint64 | single | miss | 1 | 1,048,576 | 54.4 | 52.8 | 1.02× |
| uint64 | single | batch hit | 1 | 1,048,576 | 54.1 | 63.0 | 0.87× |
| uint64 | single | delete | 1 | 1,048,576 | 177.8 | 180.7 | 0.95× |
| uint64 | single | scan64 | 1 | 1,048,576 | 503.2 | 601.4 | 0.81× |
| uint64 | concurrent | insert | 1 | 1,048,576 | 255.5 | 206.8 | 1.23× |
| uint64 | concurrent | hit | 1 | 1,048,576 | 130.6 | 99.1 | 1.32× |
| uint64 | concurrent | batch hit | 1 | 1,048,576 | 84.7 | 75.4 | 1.10× |
| uint64 | concurrent | insert | 8 | 1,048,576 | 223.2 | 173.6 | 1.29× |
| uint64 | concurrent | hit | 8 | 1,048,576 | 22.4 | 20.1 | 1.20× |
| uint64 | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 27.8 | 25.0 | 1.19× |
| string | single | insert | 1 | 1,048,576 | 438.5 | 392.3 | 1.10× |
| string | single | hit | 1 | 1,048,576 | 527.0 | 517.6 | 1.01× |
| string | single | miss | 1 | 1,048,576 | 218.2 | 210.3 | 1.05× |
| string | single | batch hit | 1 | 1,048,576 | 200.7 | 163.5 | 1.21× |
| string | single | delete | 1 | 1,048,576 | 385.7 | 518.4 | 0.74× |
| string | single | scan64 | 1 | 1,048,576 | 1044.4 | 1324.7 | 0.79× |
| string | concurrent | insert | 1 | 1,048,576 | 639.8 | 444.5 | 1.44× |
| string | concurrent | hit | 1 | 1,048,576 | 567.3 | 539.1 | 1.05× |
| string | concurrent | batch hit | 1 | 1,048,576 | 264.6 | 217.5 | 1.21× |
| string | concurrent | insert | 8 | 1,048,576 | 458.7 | 447.3 | 1.04× |
| string | concurrent | hit | 8 | 1,048,576 | 71.0 | 73.5 | 0.98× |
| string | concurrent | 90% reads / 10% overwrites | 8 | 1,048,576 | 79.2 | 128.8 | 0.63× |
