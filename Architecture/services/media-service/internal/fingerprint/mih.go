package fingerprint

import "math/bits"

// Multi-index hashing (plan 12.3): the 64-bit hash is split into four 16-bit
// bands. By pigeonhole, two hashes at Hamming distance d have some band at
// distance ≤ ⌊d/4⌋, so probing every band at radius 2 (all values within
// two bit flips) guarantees a hit for d ≤ 11. That is 1 + 16 + C(16,2) = 137
// probes per band, 548 per frame.

const (
	Bands       = 4
	BandBits    = 16
	ProbeRadius = 2
	// ProbesPerBand is the size of the radius-2 ball in 16 bits.
	ProbesPerBand = 1 + BandBits + BandBits*(BandBits-1)/2 // 137
	// HitHamming is τ_hit: candidate postings farther than this from the
	// query hash are dropped before voting.
	HitHamming = 12
	// GuaranteedHamming is the largest distance radius-2 probing can never
	// miss.
	GuaranteedHamming = 11
)

// Band extracts band b (0..3) of a hash; band 0 is the low 16 bits.
func Band(h uint64, b int) uint16 { return uint16(h >> (uint(b) * BandBits)) }

// MinBandDistance is the smallest per-band Hamming distance between two
// hashes: the radius that would have been needed to find the pair. Recorded
// per hit so the evaluation can compute what radius 1 or 0 would have found.
func MinBandDistance(a, b uint64) int {
	best := BandBits
	for i := 0; i < Bands; i++ {
		if d := bits.OnesCount16(Band(a, i) ^ Band(b, i)); d < best {
			best = d
		}
	}
	return best
}

// radius2Masks lists every XOR mask of weight ≤ 2 in 16 bits, distinct.
var radius2Masks = func() []uint16 {
	out := make([]uint16, 0, ProbesPerBand)
	out = append(out, 0)
	for i := 0; i < BandBits; i++ {
		out = append(out, 1<<uint(i))
	}
	for i := 0; i < BandBits; i++ {
		for j := i + 1; j < BandBits; j++ {
			out = append(out, 1<<uint(i)|1<<uint(j))
		}
	}
	return out
}()

// Probes returns the 137 distinct band values within radius 2 of v, v
// itself first.
func Probes(v uint16) []uint16 {
	out := make([]uint16, len(radius2Masks))
	for i, m := range radius2Masks {
		out[i] = v ^ m
	}
	return out
}

// ProbeSet is the de-duplicated set of band values one lookup needs, per
// band, for a whole list of query hashes.
type ProbeSet [Bands][]uint16

// BuildProbeSet unions the radius-2 balls of every query hash per band.
func BuildProbeSet(hashes []uint64) ProbeSet {
	var out ProbeSet
	for b := 0; b < Bands; b++ {
		seen := make(map[uint16]struct{}, len(hashes)*ProbesPerBand)
		for _, h := range hashes {
			for _, p := range Probes(Band(h, b)) {
				if _, dup := seen[p]; dup {
					continue
				}
				seen[p] = struct{}{}
				out[b] = append(out[b], p)
			}
		}
	}
	return out
}
