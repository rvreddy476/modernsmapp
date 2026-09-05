package processing

import "testing"

// A phone held upright records a landscape coded frame plus a display
// rotation. The size we store must be what the viewer sees, or the asset
// says "landscape" about a portrait picture (Tube thumbnail, 2026-09-05).
func TestDisplayDimensionsSwapOnQuarterTurns(t *testing.T) {
	cases := []struct {
		name          string
		w, h, rot     int
		wantW, wantH  int
		wantNormalize int
	}{
		{"no rotation", 1920, 1080, 0, 1920, 1080, 0},
		{"phone upright, side data -90", 1920, 1080, -90, 1080, 1920, 270},
		{"phone upright, side data 90", 1920, 1080, 90, 1080, 1920, 90},
		{"legacy 270", 1920, 1080, 270, 1080, 1920, 270},
		{"upside down keeps size", 1920, 1080, 180, 1920, 1080, 180},
		{"-180 keeps size", 1920, 1080, -180, 1920, 1080, 180},
		{"full turn is none", 640, 360, 360, 640, 360, 0},
		{"-270 is a quarter turn", 640, 360, -270, 360, 640, 90},
		{"non-quarter turn is ignored", 640, 360, 45, 640, 360, 0},
		{"already portrait coded, no tag", 478, 850, 0, 478, 850, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeRotation(c.rot); got != c.wantNormalize {
				t.Fatalf("normalizeRotation(%d) = %d, want %d", c.rot, got, c.wantNormalize)
			}
			w, h := displayDimensions(c.w, c.h, c.rot)
			if w != c.wantW || h != c.wantH {
				t.Fatalf("displayDimensions(%dx%d, %d) = %dx%d, want %dx%d",
					c.w, c.h, c.rot, w, h, c.wantW, c.wantH)
			}
		})
	}
}

// ffprobe 6.x prints the display matrix as side data ("rotation=-90",
// counter-clockwise); files from older muxers carry a clockwise "rotate"
// stream tag instead. Both must be understood, and side data wins.
func TestParseProbeDimensionsReadsRotation(t *testing.T) {
	cases := []struct {
		name                string
		out                 string
		wantW, wantH, wantR int
	}{
		{
			"plain file",
			"width=478\nheight=850\n",
			478, 850, 0,
		},
		{
			"side data -90 (iPhone / Pixel upright)",
			"width=1920\nheight=1080\nrotation=-90\n",
			1920, 1080, 270,
		},
		{
			"side data 90 with section wrappers",
			"[STREAM]\nwidth=640\nheight=360\n[SIDE_DATA]\nrotation=90\n[/SIDE_DATA]\n[/STREAM]\n",
			640, 360, 90,
		},
		{
			"fractional side data",
			"width=1280\nheight=720\nrotation=-90.00\n",
			1280, 720, 270,
		},
		{
			"legacy rotate tag is clockwise",
			"width=1920\nheight=1080\nTAG:rotate=90\n",
			1920, 1080, 270,
		},
		{
			"side data wins over tag",
			"width=1920\nheight=1080\nrotation=180\nTAG:rotate=90\n",
			1920, 1080, 180,
		},
		{
			"garbage rotation is no rotation",
			"width=1920\nheight=1080\nrotation=abc\n",
			1920, 1080, 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h, r := parseProbeDimensions(c.out)
			if w != c.wantW || h != c.wantH || r != c.wantR {
				t.Fatalf("parseProbeDimensions = %dx%d rot %d, want %dx%d rot %d",
					w, h, r, c.wantW, c.wantH, c.wantR)
			}
			dw, dh := displayDimensions(w, h, r)
			ew, eh := displayDimensions(c.wantW, c.wantH, c.wantR)
			if dw != ew || dh != eh {
				t.Fatalf("display = %dx%d, want %dx%d", dw, dh, ew, eh)
			}
		})
	}
}

// The operator override accepts quarter turns only; "0" is the plain
// re-run.
func TestValidRotationOverride(t *testing.T) {
	for _, ok := range []int{0, 90, 180, 270, -90, -180, -270, 360, 450} {
		if !ValidRotationOverride(ok) {
			t.Fatalf("%d should be a valid override", ok)
		}
	}
	for _, bad := range []int{1, 45, 89, 91, -45, 100} {
		if ValidRotationOverride(bad) {
			t.Fatalf("%d should be rejected", bad)
		}
	}
}
