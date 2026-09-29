package fingerprint

import (
	"math/bits"
	"math/rand"
	"testing"
)

// T9-3: 137 distinct probes per band, 548 per frame.
func TestProbeCount(t *testing.T) {
	if ProbesPerBand != 137 {
		t.Fatalf("ProbesPerBand = %d", ProbesPerBand)
	}
	p := Probes(0xbeef)
	if len(p) != 137 {
		t.Fatalf("%d probes", len(p))
	}
	seen := map[uint16]bool{}
	for _, v := range p {
		if seen[v] {
			t.Fatalf("duplicate probe %#x", v)
		}
		seen[v] = true
		if d := bits.OnesCount16(v ^ 0xbeef); d > ProbeRadius {
			t.Fatalf("probe %#x at distance %d", v, d)
		}
	}
	if p[0] != 0xbeef {
		t.Fatal("the value itself must be the first probe")
	}
	set := BuildProbeSet([]uint64{0x1234_5678_9abc_def0})
	total := 0
	for b := 0; b < Bands; b++ {
		total += len(set[b])
	}
	if total != 4*137 {
		t.Fatalf("%d probes per frame, want 548", total)
	}
}

// probeHits reports whether radius-2 probing of a finds b in some band.
func probeHits(a, b uint64) bool {
	for band := 0; band < Bands; band++ {
		if bits.OnesCount16(Band(a, band)^Band(b, band)) <= ProbeRadius {
			return true
		}
	}
	return false
}

// T9-1: a 3/3/2/2 split. Radius 0 and 1 miss it; radius 2 finds it, and the
// recorded minimum band distance is 2.
func TestThreeThreeTwoTwoSplitIsFound(t *testing.T) {
	a := uint64(0x0f0f_f0f0_00ff_ff00)
	// Flip 3 bits in band 0, 3 in band 1, 2 in band 2, 2 in band 3.
	flip := uint64(0x7)<<0 | uint64(0x7)<<16 | uint64(0x3)<<32 | uint64(0x3)<<48
	b := a ^ flip
	if Hamming(a, b) != 10 {
		t.Fatalf("distance %d, want 10", Hamming(a, b))
	}
	if !probeHits(a, b) {
		t.Fatal("radius 2 must find a 3/3/2/2 split")
	}
	if MinBandDistance(a, b) != 2 {
		t.Fatalf("min band distance %d, want 2", MinBandDistance(a, b))
	}
	// Radius 1 and 0 would not: every band differs by ≥ 2.
	for band := 0; band < Bands; band++ {
		if bits.OnesCount16(Band(a, band)^Band(b, band)) <= 1 {
			t.Fatalf("band %d within radius 1; the split is wrong", band)
		}
	}
	// The probe set built from a contains b's band-2 value.
	set := BuildProbeSet([]uint64{a})
	found := false
	for _, v := range set[2] {
		if v == Band(b, 2) {
			found = true
		}
	}
	if !found {
		t.Fatal("band 2 value of b is not in the probe set of a")
	}
}

// T9-2: radius 2 always hits for d ∈ [0, 11]; at d = 12 the hit rate is the
// combinatorial value (some 3/3/3/3 splits are missed).
func TestRadiusTwoGuarantee(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	flipBits := func(h uint64, d int) uint64 {
		perm := r.Perm(64)
		for _, p := range perm[:d] {
			h ^= 1 << uint(p)
		}
		return h
	}
	for d := 0; d <= GuaranteedHamming; d++ {
		for i := 0; i < 2000; i++ {
			a := r.Uint64()
			b := flipBits(a, d)
			if !probeHits(a, b) {
				t.Fatalf("d=%d: radius 2 missed %#x vs %#x", d, a, b)
			}
			if MinBandDistance(a, b) > ProbeRadius {
				t.Fatalf("d=%d: min band distance %d", d, MinBandDistance(a, b))
			}
		}
	}
	// d = 12: a miss happens only when every band has exactly 3 flips. The
	// number of 3/3/3/3 placements over C(64,12) placements:
	// C(16,3)^4 / C(64,12) ≈ 0.0316, so ~96.8% found.
	hits := 0
	const trials = 20000
	for i := 0; i < trials; i++ {
		a := r.Uint64()
		if probeHits(a, flipBits(a, 12)) {
			hits++
		}
	}
	rate := float64(hits) / trials
	const want = 1 - 98_560_000_000.0/3_284_214_703_056.0 // 1 - C(16,3)^4 / C(64,12)
	if rate < want-0.01 || rate > want+0.01 {
		t.Fatalf("d=12 hit rate %.4f, want %.4f ± 0.01", rate, want)
	}
}
