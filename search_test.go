package bbtree

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestColumnBounds checks the architecture's column searches against the
// portable reference on sorted columns padded with all-ones windows, as
// reindex leaves them.
func TestColumnBounds(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 20000 {
		count := rng.IntN(capacity + 1)
		// Few distinct values make runs of equal windows; extremes test the
		// unsigned comparisons and windows equal to the padding.
		spread := []uint64{4, 1 << 16, 1 << 32}[rng.IntN(3)]
		words := make([]uint32, count)
		for i := range words {
			switch w := uint32(rng.Uint64N(spread)); rng.IntN(8) {
			case 0:
				words[i] = 0
			case 1:
				words[i] = ^uint32(0)
			case 2:
				words[i] = 0x8000_7fff
			default:
				words[i] = w
			}
		}
		slices.Sort(words)
		var high, low [capacity]uint16
		for i := range capacity {
			w := ^uint32(0)
			if i < count {
				w = words[i]
			}
			high[i], low[i] = uint16(w>>16), uint16(w)
		}
		targets := []uint32{0, ^uint32(0), 0xffff_0000, uint32(rng.Uint64N(spread))}
		if count > 0 {
			w := words[rng.IntN(count)]
			targets = append(targets, w, w-1, w+1, w^0x8000, w^0x8000_0000)
		}
		for _, target := range targets {
			lo, hi := scalarBounds32(&high, &low, target)
			lo, hi = min(lo, count), min(hi, count)
			if glo, ghi := wideBounds(&high, &low, target, count); glo != lo || ghi != hi {
				t.Fatalf("wideBounds(%x, count %d) = [%d, %d), want [%d, %d) in %x", target, count, glo, ghi, lo, hi, words)
			}
			narrow := uint16(target >> 16)
			lo, hi = scalarBounds(&high, narrow)
			lo, hi = min(lo, count), min(hi, count)
			if glo, ghi := inlineBounds(&high, narrow, count); glo != lo || ghi != hi {
				t.Fatalf("inlineBounds(%x, count %d) = [%d, %d), want [%d, %d) in %x", narrow, count, glo, ghi, lo, hi, high)
			}
		}
	}
}
