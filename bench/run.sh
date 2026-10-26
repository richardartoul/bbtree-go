#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache="${BBTREE_BENCH_CACHE:-/tmp/bbtree-go-bench}"
revision=8adae97a0ed97e5c98fa2790312eb0aec9719846
mkdir -p "$cache/reference/src"
if [[ ! -d "$cache/upstream/.git" ]]; then
    git clone --quiet https://github.com/XiangpengHao/bbtree "$cache/upstream"
fi
git -C "$cache/upstream" checkout --quiet "$revision"
cp "$root/bench/reference.rs" "$cache/reference/src/main.rs"
cat > "$cache/reference/Cargo.toml" <<EOF
[package]
name = "bbtree-reference"
version = "0.0.0"
edition = "2024"
[dependencies]
bbtree = { path = "$cache/upstream/bbtree" }
[profile.release]
lto = "fat"
codegen-units = 1
debug = 1
EOF
go build -o "$cache/go-bench" "$root/bench"
RUSTFLAGS="-C target-cpu=native" cargo build --quiet --release --manifest-path "$cache/reference/Cargo.toml"
echo 'language,kind,mode,operation,threads,entries,operations,ns/op,checksum,timed_allocated_bytes'
# Pass exactly the same flags to both binaries. CPU pinning applies to the
# entire script via taskset; do not compare runs under different CPU sets.
"$cache/go-bench" "$@"
"$cache/reference/target/release/bbtree-reference" "$@"
