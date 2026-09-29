package fingerprint

import "testing"

// Sensitivity of the hash to a small bright overlay (a logo) and to small
// translations, on block-noise and 1/f frames, so a regression in overlay
// robustness is caught without ffmpeg. The overlay bound is asserted (the
// window exists for it); translations are inherent to a DCT hash and are
// logged, with a loose ceiling.
func TestPHashOverlayAndTranslationSensitivity(t *testing.T) {
	gens := map[string]func(int64) Grey{
		"blocknoise": func(s int64) Grey { return noiseFrame(FrameSide, s) },
		"natural":    func(s int64) Grey { return naturalFrame(FrameSide, s) },
	}
	for name, gen := range gens {
		boxWorst, box5Worst, shiftWorst := 0, 0, 0
		var boxSum, unrelatedSum float64
		const n = 40
		for seed := int64(1); seed <= n; seed++ {
			base := gen(seed)
			h0, _, _ := PHash(base)
			h1, _, _ := PHash(overlayBox(base, 2, 2, 12, 7, 255))  // ≈2% white corner box
			h5, _, _ := PHash(overlayBox(base, 2, 2, 20, 11, 255)) // ≈5%
			h2, _, _ := PHash(translate(base, 2))
			hu, _, _ := PHash(gen(seed + 1000))
			d := Hamming(h0, h1)
			boxSum += float64(d)
			if d > boxWorst {
				boxWorst = d
			}
			if d := Hamming(h0, h5); d > box5Worst {
				box5Worst = d
			}
			if d := Hamming(h0, h2); d > shiftWorst {
				shiftWorst = d
			}
			unrelatedSum += float64(Hamming(h0, hu))
		}
		t.Logf("%s: 2%% corner box mean %.1f worst %d; 5%% box worst %d; 2 px shift worst %d; unrelated mean %.1f",
			name, boxSum/n, boxWorst, box5Worst, shiftWorst, unrelatedSum/n)
		if boxWorst > AlignHamming {
			t.Errorf("%s: a 2%% corner overlay moved a frame past τ (worst %d)", name, boxWorst)
		}
		if shiftWorst > 2*AlignHamming {
			t.Errorf("%s: a 2 px translation moved a frame by %d bits", name, shiftWorst)
		}
		if unrelatedSum/n < 26 {
			t.Errorf("%s: unrelated frames average only %.1f bits apart", name, unrelatedSum/n)
		}
	}
}
