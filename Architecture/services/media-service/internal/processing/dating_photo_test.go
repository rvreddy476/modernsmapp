package processing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"testing"

	"github.com/disintegration/imaging"
)

// Lane D6 — what a served dating photo may contain.

const gpsSecret = "GPS-SECRET-17.3850N-78.4867E"

// testJPEGWithEXIF encodes a w×h patterned JPEG and inserts, right after SOI,
// an EXIF APP1 segment carrying an orientation, an ImageDescription holding
// gpsSecret and a GPS IFD (latitude ref + latitude), plus a COM segment with
// comment (skipped when empty).
func testJPEGWithEXIF(t *testing.T, w, h int, orientation uint16, comment string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(0)
			if (x/4+y/4)%2 == 0 {
				v = 255
			}
			img.Set(x, y, color.RGBA{R: v, G: uint8(x * 255 / w), B: uint8(y * 255 / h), A: 255})
		}
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return insertSegments(enc.Bytes(), exifSegment(orientation), commentSegment(comment))
}

func exifSegment(orientation uint16) []byte {
	le := binary.LittleEndian
	desc := append([]byte(gpsSecret), 0)
	tiff := make([]byte, 0, 160)
	u16 := func(v uint16) { tiff = le.AppendUint16(tiff, v) }
	u32 := func(v uint32) { tiff = le.AppendUint32(tiff, v) }
	const ifd0 = 8
	ifd0Size := 2 + 3*12 + 4
	descOff := uint32(ifd0 + ifd0Size)
	gpsOff := descOff + uint32(len(desc))
	if gpsOff%2 == 1 {
		gpsOff++
	}
	gpsSize := uint32(2 + 2*12 + 4)
	ratOff := gpsOff + gpsSize

	tiff = append(tiff, 'I', 'I')
	u16(42)
	u32(ifd0)
	u16(3)
	u16(0x010E) // ImageDescription
	u16(2)
	u32(uint32(len(desc)))
	u32(descOff)
	u16(0x0112) // Orientation
	u16(3)
	u32(1)
	u16(orientation)
	u16(0)
	u16(0x8825) // GPSInfo IFD pointer
	u16(4)
	u32(1)
	u32(gpsOff)
	u32(0)
	tiff = append(tiff, desc...)
	for uint32(len(tiff)) < gpsOff {
		tiff = append(tiff, 0)
	}
	u16(2)
	u16(0x0001) // GPSLatitudeRef
	u16(2)
	u32(2)
	tiff = append(tiff, 'N', 0, 0, 0)
	u16(0x0002) // GPSLatitude
	u16(5)
	u32(3)
	u32(ratOff)
	u32(0)
	for _, r := range [][2]uint32{{17, 1}, {23, 1}, {60, 10}} {
		u32(r[0])
		u32(r[1])
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func commentSegment(comment string) []byte {
	if comment == "" {
		return nil
	}
	seg := []byte{0xFF, 0xFE, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(comment)+2))
	return append(seg, comment...)
}

func insertSegments(jpegBytes []byte, segs ...[]byte) []byte {
	out := append([]byte{}, jpegBytes[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, jpegBytes[2:]...)
}

func assertServable(t *testing.T, name string, r RenderedImage) {
	t.Helper()
	if JPEGHasMetadata(r.Bytes) {
		t.Fatalf("%s still carries a metadata segment", name)
	}
	for _, leak := range []string{gpsSecret, "Exif", "ATPOST-"} {
		if bytes.Contains(r.Bytes, []byte(leak)) {
			t.Fatalf("%s bytes contain %q", name, leak)
		}
	}
	if _, err := jpeg.Decode(bytes.NewReader(r.Bytes)); err != nil {
		t.Fatalf("%s is not a decodable JPEG: %v", name, err)
	}
}

func TestPrepareDatingImage_StripsEXIFAndGPSAndKeepsOrientation(t *testing.T) {
	// Orientation 6: the camera stored the picture rotated; displayed it is
	// taller than wide.
	in := testJPEGWithEXIF(t, 1200, 600, 6, "ATPOST-FACE-TEST:v1:faces=1:subject=owner\n")
	if !JPEGHasMetadata(in) || !bytes.Contains(in, []byte(gpsSecret)) {
		t.Fatal("fixture does not carry EXIF/GPS; the test would prove nothing")
	}
	out, err := PrepareDatingImage(in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertServable(t, "original", out.Original)
	if out.Original.Width != 600 || out.Original.Height != 1200 {
		t.Fatalf("original is %dx%d, want 600x1200 (EXIF orientation applied before stripping)", out.Original.Width, out.Original.Height)
	}
	names := map[string]bool{}
	for _, v := range out.Variants {
		assertServable(t, v.Name, v)
		names[v.Name] = true
	}
	if !names["thumb_150"] || !names["small_480"] || !names["medium_1080"] {
		t.Fatalf("renditions = %v, want thumb_150, small_480, medium_1080", names)
	}
	assertServable(t, DatingBlurVariant, out.Blurred)
	if out.Blurred.Name != DatingBlurVariant || out.Blurred.Width > datingBlurMaxSize || out.Blurred.Height > datingBlurMaxSize {
		t.Fatalf("blurred = %s %dx%d, want %s within %dpx", out.Blurred.Name, out.Blurred.Width, out.Blurred.Height, DatingBlurVariant, datingBlurMaxSize)
	}
}

// edgeEnergy is the mean absolute difference between horizontally adjacent
// pixels (luma): high for a sharp checkerboard, near zero once blurred.
func edgeEnergy(t *testing.T, img image.Image) float64 {
	t.Helper()
	b := img.Bounds()
	var sum float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X + 1; x < b.Max.X; x++ {
			r1, g1, b1, _ := img.At(x-1, y).RGBA()
			r2, g2, b2, _ := img.At(x, y).RGBA()
			l1 := 0.299*float64(r1) + 0.587*float64(g1) + 0.114*float64(b1)
			l2 := 0.299*float64(r2) + 0.587*float64(g2) + 0.114*float64(b2)
			sum += math.Abs(l1 - l2)
			n++
		}
	}
	return sum / float64(n)
}

func TestPrepareDatingImage_BlurredVariantIsBlurred(t *testing.T) {
	in := testJPEGWithEXIF(t, 960, 960, 1, "")
	out, err := PrepareDatingImage(in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	blurred, err := jpeg.Decode(bytes.NewReader(out.Blurred.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := jpeg.Decode(bytes.NewReader(in))
	// The same downscale without the blur, as the comparison.
	plain := imaging.Fit(src, datingBlurMaxSize, datingBlurMaxSize, imaging.Linear)
	eb, ep := edgeEnergy(t, blurred), edgeEnergy(t, plain)
	if eb >= ep*0.2 {
		t.Fatalf("blurred edge energy %.0f is not well below the plain downscale's %.0f", eb, ep)
	}
}

func TestPrepareDatingImage_RefusesNonImagesAndBombs(t *testing.T) {
	if _, err := PrepareDatingImage([]byte("not an image")); !errors.Is(err, ErrDatingImageUnsupported) {
		t.Fatalf("garbage: err=%v, want ErrDatingImageUnsupported", err)
	}
	if _, err := PrepareDatingImage(nil); !errors.Is(err, ErrDatingImageUnsupported) {
		t.Fatalf("empty: err=%v", err)
	}
	// A header claiming 20000x20000 is refused from the header alone.
	var enc bytes.Buffer
	_ = jpeg.Encode(&enc, image.NewGray(image.Rect(0, 0, 8, 8)), nil)
	bomb := enc.Bytes()
	if i := bytes.Index(bomb, []byte{0xFF, 0xC0}); i >= 0 {
		binary.BigEndian.PutUint16(bomb[i+5:], 20000)
		binary.BigEndian.PutUint16(bomb[i+7:], 20000)
	}
	if _, err := PrepareDatingImage(bomb); !errors.Is(err, ErrDatingImageUnsupported) {
		t.Fatalf("oversized header: err=%v, want ErrDatingImageUnsupported", err)
	}
}

func TestJPEGHasMetadata(t *testing.T) {
	var enc bytes.Buffer
	_ = jpeg.Encode(&enc, image.NewGray(image.Rect(0, 0, 4, 4)), nil)
	plain := enc.Bytes()
	if JPEGHasMetadata(plain) {
		t.Fatal("Go-encoded JPEG reported metadata")
	}
	if !JPEGHasMetadata(insertSegments(plain, exifSegment(1))) {
		t.Fatal("EXIF APP1 not detected")
	}
	if !JPEGHasMetadata(insertSegments(plain, commentSegment("hello"))) {
		t.Fatal("COM segment not detected")
	}
	if JPEGHasMetadata([]byte("PNG")) {
		t.Fatal("non-JPEG reported metadata")
	}
}
