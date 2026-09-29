package fingerprint

import (
	"math"
	"math/rand"
	"testing"
)

// Synthetic frames. gradientFrame is a smooth luma ramp; noiseFrame is
// pseudo-random texture from a seed; flatFrame is one value everywhere.

func flatFrame(side int, v uint8) Grey {
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	for i := range g.Pix {
		g.Pix[i] = v
	}
	return g
}

func gradientFrame(side int) Grey {
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			g.Pix[y*side+x] = uint8((x*255/side + y*128/side) % 256)
		}
	}
	return g
}

func noiseFrame(side int, seed int64) Grey {
	r := rand.New(rand.NewSource(seed))
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	// Low-frequency noise: blocks of 8×8 so the DCT block carries energy.
	for by := 0; by < side; by += 8 {
		for bx := 0; bx < side; bx += 8 {
			v := uint8(r.Intn(256))
			for y := by; y < by+8 && y < side; y++ {
				for x := bx; x < bx+8 && x < side; x++ {
					g.Pix[y*side+x] = v
				}
			}
		}
	}
	return g
}

// Golden: the DCT is deterministic. The hash of the gradient frame is pinned
// so an accidental change to the transform, the zigzag positions or the
// median rule fails here instead of silently invalidating every stored
// fingerprint (which would need an AlgoVersion bump).
func TestPHashGolden(t *testing.T) {
	h, st, err := PHash(gradientFrame(FrameSide))
	if err != nil {
		t.Fatal(err)
	}
	const want = uint64(0x6c99274992a66dd9)
	if h != want {
		t.Fatalf("gradient pHash = %#x, want %#x (AlgoVersion bump needed if intentional)", h, want)
	}
	if st.Stddev < FlatStddev {
		t.Fatalf("gradient stddev %.2f reads as flat", st.Stddev)
	}
	if FlatFromStats(st) {
		t.Fatal("gradient frame is flat")
	}
}

func TestPHashRejectsWrongSize(t *testing.T) {
	if _, _, err := PHash(flatFrame(32, 10)); err == nil {
		t.Fatal("a 32×32 frame must be refused")
	}
}

func TestFlatFrameIsFlagged(t *testing.T) {
	_, st, err := PHash(flatFrame(FrameSide, 200))
	if err != nil {
		t.Fatal(err)
	}
	if !FlatFromStats(st) {
		t.Fatalf("uniform frame not flat: %+v", st)
	}
	// Barely-varying noise (stddev < 6) is flat too.
	g := flatFrame(FrameSide, 100)
	r := rand.New(rand.NewSource(1))
	for i := range g.Pix {
		g.Pix[i] = uint8(100 + r.Intn(3))
	}
	if _, st, _ = PHash(g); !FlatFromStats(st) {
		t.Fatalf("stddev %.2f frame not flat", st.Stddev)
	}
}

// The median rule puts (about) half the bits on: a hash is never
// degenerate for a textured frame.
func TestPHashHalfBitsOn(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		h, _, _ := PHash(noiseFrame(FrameSide, seed))
		if IsDegenerate(h) {
			t.Fatalf("seed %d: textured frame hashed degenerate %#x", seed, h)
		}
	}
}

// Robustness: the same picture, brighter, darker or lightly noised, hashes
// within the alignment threshold; a different picture does not.
func TestPHashInvariances(t *testing.T) {
	base := noiseFrame(FrameSide, 7)
	h0, _, _ := PHash(base)
	brighter := Grey{Side: FrameSide, Pix: make([]uint8, len(base.Pix))}
	darker := Grey{Side: FrameSide, Pix: make([]uint8, len(base.Pix))}
	noisy := Grey{Side: FrameSide, Pix: make([]uint8, len(base.Pix))}
	r := rand.New(rand.NewSource(99))
	for i, p := range base.Pix {
		brighter.Pix[i] = uint8(math.Min(255, float64(p)*1.3+20))
		darker.Pix[i] = uint8(float64(p) * 0.6)
		noisy.Pix[i] = uint8(math.Max(0, math.Min(255, float64(p)+float64(r.Intn(21)-10))))
	}
	for name, g := range map[string]Grey{"brighter": brighter, "darker": darker, "noisy": noisy} {
		h, _, _ := PHash(g)
		if d := Hamming(h0, h); d > AlignHamming {
			t.Errorf("%s: Hamming %d > %d", name, d, AlignHamming)
		}
	}
	other, _, _ := PHash(noiseFrame(FrameSide, 8))
	if d := Hamming(h0, other); d < 16 {
		t.Errorf("different picture at Hamming %d; too close", d)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []Frame{{0, 0x0123456789abcdef, FlagFlat}, {500, math.MaxUint64, FlagStaticRepeat | FlagDegenerate}, {-1, 0, 0}}
	b := Encode(in)
	if len(b) != len(in)*FrameBytes {
		t.Fatalf("encoded %d bytes, want %d", len(b), len(in)*FrameBytes)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		if in[i] != out[i] {
			t.Fatalf("frame %d: %+v != %+v", i, in[i], out[i])
		}
	}
	if _, err := Decode(b[:len(b)-1]); err == nil {
		t.Fatal("a partial frame must be refused")
	}
}

func TestResampleCropsAndAverages(t *testing.T) {
	// Left half black, right half white at 128; cropping to the right half
	// yields an all-white 64×64.
	g := Grey{Side: 128, Pix: make([]uint8, 128*128)}
	for y := 0; y < 128; y++ {
		for x := 64; x < 128; x++ {
			g.Pix[y*128+x] = 255
		}
	}
	right := Resample(g, 64, 0, 128, 128, FrameSide)
	for i, p := range right.Pix {
		if p != 255 {
			t.Fatalf("pixel %d = %d, want 255", i, p)
		}
	}
	whole := Resample(g, 0, 0, 128, 128, FrameSide)
	if whole.Pix[0] != 0 || whole.Pix[63] != 255 {
		t.Fatalf("whole resample edges %d/%d", whole.Pix[0], whole.Pix[63])
	}
	// A bad rectangle falls back to the full frame rather than panicking.
	bad := Resample(g, 100, 0, 50, 128, FrameSide)
	if bad.Pix[0] != 0 || bad.Pix[63] != 255 {
		t.Fatal("invalid rectangle did not fall back to the full frame")
	}
}

func TestIsDegenerate(t *testing.T) {
	cases := map[uint64]bool{0: true, 0xf: true, 0x1f: false, math.MaxUint64: true, math.MaxUint64 >> 4: true, math.MaxUint64 >> 5: false}
	for h, want := range cases {
		if got := IsDegenerate(h); got != want {
			t.Errorf("IsDegenerate(%#x) = %v, want %v", h, got, want)
		}
	}
}
