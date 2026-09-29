// Package fingerprint is the visual matcher behind Copyright Match phase 1
// (docs/designs/copyright-match-plan.md, section 12). It is pure Go except
// for extract.go, which drives ffmpeg. Audio is deliberately absent.
//
// Per frame: a 64-bit pHash of the border-cropped, 64×64 grey frame at 2 fps
// (hash.go, crop.go). Per video: informative weights, anchors, duration
// bucket and query frames (weights.go); a 4×16-bit multi-index probed at
// radius 2 (mih.go); voting, alignment, coverage and classification
// (match.go).
package fingerprint

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
)

// AlgoVersion is part of every key the index and the pairs use. Bump it when
// anything in this package changes what a hash or a weight means; the old
// version is then dual-written by backfill and retired (plan 6.1, rule 5).
const AlgoVersion = 1

// FPS is the frame rate of every sequence, indexed side and query side.
const FPS = 2

// FrameMs is the time between two frames.
const FrameMs = 1000 / FPS

// FrameSide is the edge of the grey frame the hash is computed from.
const FrameSide = 64

// Flag bits on a frame.
const (
	// FlagFlat: luma stddev < FlatStddev, or the AC energy of the DCT block
	// is below FlatACEnergy. Weight 0 on both sides; never indexed.
	FlagFlat uint8 = 1 << iota
	// FlagStaticRepeat: Hamming ≤ StaticHamming to the previous frame. Kept
	// for alignment; each static run contributes at most StaticRunCapMs of
	// weight, so a slideshow keeps its timeline without dominating coverage.
	FlagStaticRepeat
	// FlagDegenerate: popcount ≤ DegenerateLow or ≥ DegenerateHigh. Weighted
	// normally, but never indexed as an anchor (plan 12.3).
	FlagDegenerate
)

const (
	FlatStddev     = 6.0
	FlatACEnergy   = 1.0 // mean |AC coefficient| over the 64 hashed positions
	StaticHamming  = 2
	StaticRunCapMs = 2000
	DegenerateLow  = 4
	DegenerateHigh = 60
)

// Frame is one hashed frame of a sequence.
type Frame struct {
	TMs   int32
	Hash  uint64
	Flags uint8
}

// FrameBytes is the stored size of one frame:
// [t_ms int32 LE | hash uint64 LE | flags uint8].
const FrameBytes = 4 + 8 + 1

// Encode serialises frames for copyright_fingerprints.frames.
func Encode(frames []Frame) []byte {
	out := make([]byte, 0, len(frames)*FrameBytes)
	var buf [FrameBytes]byte
	for _, f := range frames {
		binary.LittleEndian.PutUint32(buf[0:4], uint32(f.TMs))
		binary.LittleEndian.PutUint64(buf[4:12], f.Hash)
		buf[12] = f.Flags
		out = append(out, buf[:]...)
	}
	return out
}

// Decode is the inverse of Encode. It refuses a length that is not a whole
// number of frames rather than truncating silently.
func Decode(b []byte) ([]Frame, error) {
	if len(b)%FrameBytes != 0 {
		return nil, fmt.Errorf("fingerprint: %d bytes is not a whole number of %d-byte frames", len(b), FrameBytes)
	}
	out := make([]Frame, len(b)/FrameBytes)
	for i := range out {
		p := b[i*FrameBytes:]
		out[i] = Frame{
			TMs:   int32(binary.LittleEndian.Uint32(p[0:4])),
			Hash:  binary.LittleEndian.Uint64(p[4:12]),
			Flags: p[12],
		}
	}
	return out, nil
}

// Hamming is the bit distance between two hashes.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// IsDegenerate reports a hash the index must never hold (plan 12.3).
func IsDegenerate(h uint64) bool {
	pc := bits.OnesCount64(h)
	return pc <= DegenerateLow || pc >= DegenerateHigh
}

// Grey is a square 8-bit luma frame, row-major, Side×Side.
type Grey struct {
	Side int
	Pix  []uint8
}

// hashPositions are the 8×8 lowest-frequency block of the 32×32 DCT, row
// by row, DC included. DC is always above the median, so bit 0 is
// constant: the hash is effectively 63 bits, which the pigeonhole bound
// of the multi-index tolerates. Chosen over a zigzag set reaching single-
// axis frequency 10 for lower sensitivity to small translations (measured
// in hash_experiment_test.go). Fixed for AlgoVersion 1.
var hashPositions = func() [64][2]int {
	var out [64][2]int
	n := 0
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			out[n] = [2]int{u, v}
			n++
		}
	}
	return out
}()

// WindowTaper is the Tukey taper fraction applied to the mean-centred luma
// before the DCT: the outer 40% of each axis fades to the frame mean, so a
// corner logo or a channel bug carries little weight. Measured
// (hash_experiment_test.go): a 2% white corner box moves the hash by ~2-3
// bits windowed against ~14 unwindowed; unrelated pairs stay at ~31.
const WindowTaper = 0.8

// tukeyWindow[i] for i in [0, FrameSide).
var tukeyWindow = func() [FrameSide]float64 {
	var w [FrameSide]float64
	a := WindowTaper
	for i := range w {
		x := float64(i) / float64(FrameSide-1)
		switch {
		case x < a/2:
			w[i] = 0.5 * (1 + math.Cos(2*math.Pi/a*(x-a/2)))
		case x > 1-a/2:
			w[i] = 0.5 * (1 + math.Cos(2*math.Pi/a*(x-1+a/2)))
		default:
			w[i] = 1
		}
	}
	return w
}()

// dctCos[k][n] = cos(π (2n+1) k / (2N)) for N = 32.
var dctCos = func() [32][32]float64 {
	var c [32][32]float64
	for k := 0; k < 32; k++ {
		for n := 0; n < 32; n++ {
			c[k][n] = math.Cos(math.Pi * float64(2*n+1) * float64(k) / 64)
		}
	}
	return c
}()

// Stats are the per-frame numbers the flags are decided on.
type Stats struct {
	Stddev   float64
	ACEnergy float64
}

// dctNeed is how many frequencies per axis the DCT computes (the hash
// reads 8; the experiment test compares wider sets).
const dctNeed = 12

// dctCoefficients averages the 64×64 frame 2×2 to 32×32 and returns the
// low dctNeed×dctNeed block of its separable DCT-II, plus the luma stddev.
func dctCoefficients(g Grey) (coef [dctNeed][dctNeed]float64, stddev float64) {
	var sum, sumSq float64
	for _, p := range g.Pix {
		v := float64(p)
		sum += v
		sumSq += v * v
	}
	n := float64(len(g.Pix))
	mean := sum / n
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}
	stddev = math.Sqrt(variance)

	// Window around the mean, then 2×2 mean → 32×32.
	var small [32][32]float64
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			var acc float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					py, px := 2*y+dy, 2*x+dx
					acc += mean + (float64(g.Pix[py*FrameSide+px])-mean)*tukeyWindow[px]*tukeyWindow[py]
				}
			}
			small[y][x] = acc / 4
		}
	}
	var rows [32][dctNeed]float64
	for y := 0; y < 32; y++ {
		for v := 0; v < dctNeed; v++ {
			var acc float64
			for x := 0; x < 32; x++ {
				acc += small[y][x] * dctCos[v][x]
			}
			rows[y][v] = acc
		}
	}
	for u := 0; u < dctNeed; u++ {
		for v := 0; v < dctNeed; v++ {
			var acc float64
			for y := 0; y < 32; y++ {
				acc += rows[y][v] * dctCos[u][y]
			}
			coef[u][v] = acc
		}
	}
	return coef, stddev
}

// hashFromCoefficients thresholds the coefficients at the positions
// against their median. Returns the hash and the mean |coefficient|.
func hashFromCoefficients(coef [dctNeed][dctNeed]float64, positions [64][2]int) (uint64, float64) {
	var vals [64]float64
	var energy float64
	for i, p := range positions {
		vals[i] = coef[p[0]][p[1]]
		energy += math.Abs(vals[i])
	}
	median := medianOf64(vals)
	var h uint64
	for i := 0; i < 64; i++ {
		if vals[i] > median {
			h |= 1 << uint(i)
		}
	}
	return h, energy / 64
}

// PHash computes the 64-bit hash of a 64×64 grey frame and the stats the
// flat flag is decided on. The frame is windowed (WindowTaper) around its
// mean, averaged 2×2 to 32×32, transformed with a separable DCT-II, and
// each of the 64 hashPositions is compared to their median.
func PHash(g Grey) (uint64, Stats, error) {
	if g.Side != FrameSide || len(g.Pix) != FrameSide*FrameSide {
		return 0, Stats{}, fmt.Errorf("fingerprint: PHash wants a %d×%d frame, got side %d with %d pixels", FrameSide, FrameSide, g.Side, len(g.Pix))
	}
	coef, stddev := dctCoefficients(g)
	h, energy := hashFromCoefficients(coef, hashPositions)
	return h, Stats{Stddev: stddev, ACEnergy: energy}, nil
}

func medianOf64(v [64]float64) float64 {
	s := v // copy
	// insertion sort: 64 values, called at 2 fps
	for i := 1; i < 64; i++ {
		x := s[i]
		j := i - 1
		for j >= 0 && s[j] > x {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = x
	}
	return (s[31] + s[32]) / 2
}

// FlatFromStats is the flat rule (plan 12.2).
func FlatFromStats(st Stats) bool {
	return st.Stddev < FlatStddev || st.ACEnergy < FlatACEnergy
}

// Resample box-averages src (any square side) into a Side×Side frame, using
// only the rectangle [x0,x1)×[y0,y1) of the source. It is the crop + resize
// step of the pipeline; area averaging keeps a downscale from aliasing.
func Resample(src Grey, x0, y0, x1, y1, side int) Grey {
	if x1 <= x0 || y1 <= y0 || x0 < 0 || y0 < 0 || x1 > src.Side || y1 > src.Side {
		x0, y0, x1, y1 = 0, 0, src.Side, src.Side
	}
	out := Grey{Side: side, Pix: make([]uint8, side*side)}
	w := float64(x1 - x0)
	h := float64(y1 - y0)
	for oy := 0; oy < side; oy++ {
		sy0 := float64(y0) + h*float64(oy)/float64(side)
		sy1 := float64(y0) + h*float64(oy+1)/float64(side)
		for ox := 0; ox < side; ox++ {
			sx0 := float64(x0) + w*float64(ox)/float64(side)
			sx1 := float64(x0) + w*float64(ox+1)/float64(side)
			out.Pix[oy*side+ox] = boxMean(src, sx0, sy0, sx1, sy1)
		}
	}
	return out
}

// boxMean averages the source over a fractional box with area weights.
func boxMean(src Grey, sx0, sy0, sx1, sy1 float64) uint8 {
	var acc, wsum float64
	yStart, yEnd := int(math.Floor(sy0)), int(math.Ceil(sy1))
	xStart, xEnd := int(math.Floor(sx0)), int(math.Ceil(sx1))
	for y := yStart; y < yEnd && y < src.Side; y++ {
		wy := math.Min(sy1, float64(y+1)) - math.Max(sy0, float64(y))
		if wy <= 0 {
			continue
		}
		row := src.Pix[y*src.Side:]
		for x := xStart; x < xEnd && x < src.Side; x++ {
			wx := math.Min(sx1, float64(x+1)) - math.Max(sx0, float64(x))
			if wx <= 0 {
				continue
			}
			acc += float64(row[x]) * wx * wy
			wsum += wx * wy
		}
	}
	if wsum == 0 {
		return 0
	}
	return uint8(math.Round(acc / wsum))
}
