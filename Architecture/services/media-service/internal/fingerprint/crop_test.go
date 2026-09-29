package fingerprint

import "testing"

// A letterboxed frame: black bands of `band` rows top and bottom.
func letterboxed(side, band int, content Grey) Grey {
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	for y := band; y < side-band; y++ {
		copy(g.Pix[y*side:(y+1)*side], content.Pix[y*side:(y+1)*side])
	}
	return g
}

func pillarboxed(side, band int, content Grey) Grey {
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	for y := 0; y < side; y++ {
		for x := band; x < side-band; x++ {
			g.Pix[y*side+x] = content.Pix[y*side+x]
		}
	}
	return g
}

func brightNoise(side int, seed int64) Grey {
	g := noiseFrame(side, seed)
	for i := range g.Pix {
		if g.Pix[i] <= CropLumaThreshold {
			g.Pix[i] = CropLumaThreshold + 1
		}
	}
	return g
}

func TestFrameBoundsFindsLetterboxAndPillarbox(t *testing.T) {
	content := brightNoise(128, 3)
	r, ok := FrameBounds(letterboxed(128, 16, content))
	if !ok || r != (Rect{0, 16, 128, 112}) {
		t.Fatalf("letterbox bounds %+v ok=%v", r, ok)
	}
	r, ok = FrameBounds(pillarboxed(128, 20, content))
	if !ok || r != (Rect{20, 0, 108, 128}) {
		t.Fatalf("pillarbox bounds %+v ok=%v", r, ok)
	}
	if _, ok := FrameBounds(flatFrame(128, CropLumaThreshold)); ok {
		t.Fatal("an all-dark frame must not report bounds")
	}
}

func TestCropVoteMedianIgnoresFlatAndOutliers(t *testing.T) {
	v := NewCropVote(128)
	content := brightNoise(128, 4)
	// 19 letterboxed frames, one frame with a big logo in the band (an
	// outlier), plus flat frames that must not vote.
	for i := 0; i < 19; i++ {
		v.Offer(letterboxed(128, 16, content), false)
		v.Offer(flatFrame(128, 0), true)
	}
	outlier := letterboxed(128, 16, content)
	for x := 0; x < 128; x++ {
		outlier.Pix[2*128+x] = 200 // bright row inside the top band
	}
	decided := v.Offer(outlier, false)
	if !decided {
		t.Fatal("20 non-flat frames should decide the vote")
	}
	if r := v.Decide(); r != (Rect{0, 16, 128, 112}) {
		t.Fatalf("median crop %+v, want the letterbox", r)
	}
}

func TestCropVoteRefusesToCropThePicture(t *testing.T) {
	v := NewCropVote(128)
	// A tiny bright square: keeping it would crop away > 75% of each axis.
	g := flatFrame(128, 0)
	for y := 60; y < 68; y++ {
		for x := 60; x < 68; x++ {
			g.Pix[y*128+x] = 255
		}
	}
	for i := 0; i < CropSampleFrames; i++ {
		v.Offer(g, false)
	}
	if r := v.Decide(); r != Full(128) {
		t.Fatalf("crop %+v, want the full frame", r)
	}
	if r := NewCropVote(128).Decide(); r != Full(128) {
		t.Fatalf("no votes: %+v, want the full frame", r)
	}
}

func TestCropVoteLookaheadBound(t *testing.T) {
	v := NewCropVote(128)
	for i := 0; i < CropMaxLookahead-1; i++ {
		if v.Offer(flatFrame(128, 0), true) {
			t.Fatalf("decided after %d flat frames", i+1)
		}
	}
	if !v.Offer(flatFrame(128, 0), true) {
		t.Fatal("the lookahead cap must decide the vote")
	}
}

// squeezeIntoBand is what a letterbox really is: the whole picture scaled
// into the rows [band, side-band), black above and below.
func squeezeIntoBand(content Grey, band int) Grey {
	side := content.Side
	g := Grey{Side: side, Pix: make([]uint8, side*side)}
	inner := side - 2*band
	for y := band; y < side-band; y++ {
		sy := (y - band) * side / inner
		copy(g.Pix[y*side:(y+1)*side], content.Pix[sy*side:(sy+1)*side])
	}
	return g
}

// End to end through HashRawGrey: a letterboxed sequence hashes like the
// unboxed one (T10-6), and the crop decided by the vote is applied to the
// frames buffered before it closed as well as those after.
func TestHashRawGreyAppliesOneCropToEveryFrame(t *testing.T) {
	const n = CropSampleFrames + 10
	var plain, boxed []byte
	var boxedFrames []Grey
	for i := 0; i < n; i++ {
		c := brightNoise(ExtractSide, int64(100+i))
		plain = append(plain, c.Pix...)
		b := squeezeIntoBand(c, 16)
		boxed = append(boxed, b.Pix...)
		boxedFrames = append(boxedFrames, b)
	}
	rp, err := HashRawGrey(bytesReader(plain), ExtractSide, 0)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := HashRawGrey(bytesReader(boxed), ExtractSide, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rb.Crop != (Rect{0, 16, ExtractSide, 112}) {
		t.Fatalf("crop %+v", rb.Crop)
	}
	if rp.Crop != Full(ExtractSide) {
		t.Fatalf("plain crop %+v, want the full frame", rp.Crop)
	}
	if len(rp.Frames) != n || len(rb.Frames) != n || rp.MeasuredMs != n*FrameMs {
		t.Fatalf("frames %d/%d measured %d", len(rp.Frames), len(rb.Frames), rp.MeasuredMs)
	}
	for i := range rp.Frames {
		if rp.Frames[i].TMs != int32(i*FrameMs) {
			t.Fatalf("frame %d at %d ms", i, rp.Frames[i].TMs)
		}
		// Every frame — buffered during the vote or streamed after — was
		// hashed with the decided rectangle.
		want, _, _ := PHash(Resample(boxedFrames[i], rb.Crop.X0, rb.Crop.Y0, rb.Crop.X1, rb.Crop.Y1, FrameSide))
		if rb.Frames[i].Hash != want {
			t.Fatalf("frame %d was not hashed with the video's crop", i)
		}
		// The squeeze-then-crop round trip is a nearest-row resample, so
		// allow τ against the plain picture.
		if d := Hamming(rp.Frames[i].Hash, rb.Frames[i].Hash); d > AlignHamming {
			t.Fatalf("frame %d: boxed vs plain Hamming %d", i, d)
		}
	}
}
