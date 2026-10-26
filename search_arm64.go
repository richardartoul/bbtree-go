//go:build !purego

package bbtree

// fastDescent selects the assembly descent for numeric keys. Every arm64 CPU
// has the NEON compares it uses.
const fastDescent = true
