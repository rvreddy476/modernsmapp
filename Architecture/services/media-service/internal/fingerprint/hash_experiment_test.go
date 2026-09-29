package fingerprint

import (
	"math"
	"math/rand"
	"testing"
)

// Coefficient-set experiment (run with -run Experiment -v): which 64 DCT
// positions give the most robust hash on adversarial (block-noise) and
// natural-like (1/f) frames, under a bright corner overlay and small
// translations. Logged only; the chosen set is pinned by TestPHashGolden.

// naturalFrame is a sum of low-frequency sinusoids plus mild noise: a 1/f
// spectrum like photographed content.
func naturalFrame(side int, seed int64) Grey {
	r := rand.New(rand.NewSource(seed))
	type wave struct{ fx, fy, phase, amp float64 }
	var waves []wave
	for k := 1; k <= 12; k++ {
		waves = append(waves, wave{
			fx: r.Float64() * 6, fy: r.Float64() * 6, phase: r.Float64() * 2 * math.Pi,
			amp: 60 / float64(k),
		})
	}
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			v := 128.0
			for _, w := range waves {
				v += w.amp * math.Sin(2*math.Pi*(w.fx*float64(x)/float64(side)+w.fy*float64(y)/float64(side))+w.phase)
			}
			v += r.NormFloat64() * 4
			g.Pix[y*side+x] = uint8(math.Max(0, math.Min(255, v)))
		}
	}
	return g
}

// zigzagBlock takes AC positions in zigzag order preferring those with
// both axes ≤ maxAxis; when fewer than 64 qualify, the lowest remaining
// zigzag positions fill the rest.
func zigzagBlock(maxAxis, count int) [64][2]int {
	var out [64][2]int
	n := 0
	var rest [][2]int
	for s := 1; s <= 2*dctNeed && n < count; s++ {
		for u := 0; u <= s && n < count; u++ {
			v := s - u
			if u >= dctNeed || v >= dctNeed {
				continue
			}
			if u > maxAxis || v > maxAxis {
				rest = append(rest, [2]int{u, v})
				continue
			}
			out[n] = [2]int{u, v}
			n++
		}
	}
	for i := 0; n < count && i < len(rest); i++ {
		out[n] = rest[i]
		n++
	}
	return out
}

func block8x8WithDC() [64][2]int {
	var out [64][2]int
	n := 0
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			out[n] = [2]int{u, v}
			n++
		}
	}
	return out
}

func overlayBox(g Grey, x0, y0, w, h int, val uint8) Grey {
	out := Grey{Side: g.Side, Pix: append([]uint8(nil), g.Pix...)}
	for y := y0; y < y0+h && y < g.Side; y++ {
		for x := x0; x < x0+w && x < g.Side; x++ {
			out.Pix[y*g.Side+x] = val
		}
	}
	return out
}

func translate(g Grey, dx int) Grey {
	out := Grey{Side: g.Side, Pix: make([]uint8, len(g.Pix))}
	for y := 0; y < g.Side; y++ {
		for x := 0; x < g.Side; x++ {
			sx := x + dx
			if sx >= g.Side {
				sx = g.Side - 1
			}
			if sx < 0 {
				sx = 0
			}
			out.Pix[y*g.Side+x] = g.Pix[y*g.Side+sx]
		}
	}
	return out
}

func TestExperimentCoefficientSets(t *testing.T) {
	sets := map[string][64][2]int{
		"zigzag_uv<=10 (current)": hashPositions,
		"zigzag_axis<=7":          zigzagBlock(7, 64), // fills past the block with (0,8)/(8,0)-free? no: axis cap 7 keeps 63 then wraps
		"block8x8_incl_DC":        block8x8WithDC(),
		"zigzag_axis<=6":          zigzagBlock(6, 64),
	}
	gens := map[string]func(int64) Grey{
		"blocknoise": func(s int64) Grey { return noiseFrame(FrameSide, s) },
		"natural":    func(s int64) Grey { return naturalFrame(FrameSide, s) },
	}
	for setName, positions := range sets {
		for genName, gen := range gens {
			var boxSum, box5Sum, tr2Sum, tr4Sum, unrelatedSum float64
			boxWorst, tr2Worst := 0, 0
			const n = 40
			for seed := int64(1); seed <= n; seed++ {
				base := gen(seed)
				c0, _ := dctCoefficients(base)
				h0, _ := hashFromCoefficients(c0, positions)
				hash := func(g Grey) uint64 {
					c, _ := dctCoefficients(g)
					h, _ := hashFromCoefficients(c, positions)
					return h
				}
				d := Hamming(h0, hash(overlayBox(base, 2, 2, 12, 7, 255)))
				boxSum += float64(d)
				if d > boxWorst {
					boxWorst = d
				}
				box5Sum += float64(Hamming(h0, hash(overlayBox(base, 2, 2, 20, 11, 255))))
				d = Hamming(h0, hash(translate(base, 2)))
				tr2Sum += float64(d)
				if d > tr2Worst {
					tr2Worst = d
				}
				tr4Sum += float64(Hamming(h0, hash(translate(base, 4))))
				unrelatedSum += float64(Hamming(h0, hash(gen(seed+1000))))
			}
			t.Logf("%-26s %-10s box2%%: mean %.1f worst %d | box5%%: mean %.1f | shift2: mean %.1f worst %d | shift4: mean %.1f | unrelated: mean %.1f",
				setName, genName, boxSum/n, boxWorst, box5Sum/n, tr2Sum/n, tr2Worst, tr4Sum/n, unrelatedSum/n)
		}
	}
}

// windowed applies a separable Tukey window (alpha = taper fraction) to the
// luma around its mean, so corners fade to the mean: a corner logo then
// carries little weight, and both sides of a pair are windowed alike.
func windowed(g Grey, alpha float64) Grey {
	n := g.Side
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n-1)
		switch {
		case x < alpha/2:
			w[i] = 0.5 * (1 + math.Cos(2*math.Pi/alpha*(x-alpha/2)))
		case x > 1-alpha/2:
			w[i] = 0.5 * (1 + math.Cos(2*math.Pi/alpha*(x-1+alpha/2)))
		default:
			w[i] = 1
		}
	}
	var sum float64
	for _, p := range g.Pix {
		sum += float64(p)
	}
	mean := sum / float64(len(g.Pix))
	out := Grey{Side: n, Pix: make([]uint8, len(g.Pix))}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			v := mean + (float64(g.Pix[y*n+x])-mean)*w[x]*w[y]
			out.Pix[y*n+x] = uint8(math.Max(0, math.Min(255, math.Round(v))))
		}
	}
	return out
}

// TestExperimentWindowing was the measurement that chose WindowTaper. The
// hash now windows by itself, so the tapers here stack on top of it; the
// row "tukey 0.0" is the production hash and the rest show diminishing
// returns of more taper.
func TestExperimentWindowing(t *testing.T) {
	positions := block8x8WithDC()
	gens := map[string]func(int64) Grey{
		"blocknoise": func(s int64) Grey { return noiseFrame(FrameSide, s) },
		"natural":    func(s int64) Grey { return naturalFrame(FrameSide, s) },
	}
	for _, alpha := range []float64{0, 0.3, 0.5, 0.8, 1.0} {
		for genName, gen := range gens {
			var boxSum, box5Sum, tr2Sum, tr4Sum, unrelatedSum float64
			boxWorst, tr2Worst := 0, 0
			const n = 40
			hash := func(g Grey) uint64 {
				if alpha > 0 {
					g = windowed(g, alpha)
				}
				c, _ := dctCoefficients(g)
				h, _ := hashFromCoefficients(c, positions)
				return h
			}
			for seed := int64(1); seed <= n; seed++ {
				base := gen(seed)
				h0 := hash(base)
				d := Hamming(h0, hash(overlayBox(base, 2, 2, 12, 7, 255)))
				boxSum += float64(d)
				if d > boxWorst {
					boxWorst = d
				}
				box5Sum += float64(Hamming(h0, hash(overlayBox(base, 2, 2, 20, 11, 255))))
				d = Hamming(h0, hash(translate(base, 2)))
				tr2Sum += float64(d)
				if d > tr2Worst {
					tr2Worst = d
				}
				tr4Sum += float64(Hamming(h0, hash(translate(base, 4))))
				unrelatedSum += float64(Hamming(h0, hash(gen(seed+1000))))
			}
			t.Logf("tukey %.1f %-10s box2%%: mean %.1f worst %d | box5%%: mean %.1f | shift2: mean %.1f worst %d | shift4: mean %.1f | unrelated: mean %.1f",
				alpha, genName, boxSum/n, boxWorst, box5Sum/n, tr2Sum/n, tr2Worst, tr4Sum/n, unrelatedSum/n)
		}
	}
}
