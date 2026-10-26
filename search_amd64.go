//go:build !purego

package bbtree

var hasAVX512 = avx512Supported()

// fastDescent selects the assembly descent for numeric keys.
var fastDescent = hasAVX512

func avx512Supported() bool
