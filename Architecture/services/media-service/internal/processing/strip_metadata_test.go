package processing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// Security sweep #4: the stored original of an image must not carry the
// camera file's EXIF (GPS), XMP or text chunks. These build inputs WITH
// such metadata and prove the worker's rewrite drops it while keeping the
// picture — and, for a JPEG, keeps it the right way up once the
// orientation tag that said "rotate me" is gone.

// exifWithGPS is a minimal TIFF-in-APP1 payload: IFD0 with Orientation=6
// (rotate 90° clockwise) and a GPSInfo pointer to a GPS IFD holding a
// GPSLatitude rational triple. Big-endian ("MM").
func exifWithGPS() []byte {
	var b bytes.Buffer
	b.WriteString("Exif\x00\x00")
	tiff := new(bytes.Buffer)
	tiff.WriteString("MM\x00\x2a")
	binary.Write(tiff, binary.BigEndian, uint32(8)) // IFD0 offset
	// IFD0: 2 entries.
	binary.Write(tiff, binary.BigEndian, uint16(2))
	// Orientation (0x0112), SHORT, count 1, value 6.
	binary.Write(tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(tiff, binary.BigEndian, uint16(3))
	binary.Write(tiff, binary.BigEndian, uint32(1))
	binary.Write(tiff, binary.BigEndian, uint16(6))
	binary.Write(tiff, binary.BigEndian, uint16(0))
	// GPSInfo (0x8825), LONG, count 1, offset of the GPS IFD.
	gpsIFDOffset := uint32(8 + 2 + 2*12 + 4)
	binary.Write(tiff, binary.BigEndian, uint16(0x8825))
	binary.Write(tiff, binary.BigEndian, uint16(4))
	binary.Write(tiff, binary.BigEndian, uint32(1))
	binary.Write(tiff, binary.BigEndian, gpsIFDOffset)
	binary.Write(tiff, binary.BigEndian, uint32(0)) // next IFD: none
	// GPS IFD: 1 entry, GPSLatitude (0x0002), RATIONAL, count 3, at offset.
	binary.Write(tiff, binary.BigEndian, uint16(1))
	latOffset := gpsIFDOffset + 2 + 12 + 4
	binary.Write(tiff, binary.BigEndian, uint16(0x0002))
	binary.Write(tiff, binary.BigEndian, uint16(5))
	binary.Write(tiff, binary.BigEndian, uint32(3))
	binary.Write(tiff, binary.BigEndian, latOffset)
	binary.Write(tiff, binary.BigEndian, uint32(0))
	for _, v := range []uint32{17, 1, 23, 1, 4560, 100} { // 17° 23' 45.60"
		binary.Write(tiff, binary.BigEndian, v)
	}
	b.Write(tiff.Bytes())
	return b.Bytes()
}

// jpegWithExif encodes a 4x2 picture and splices an APP1 Exif segment in
// after SOI, the way a camera writes it.
func jpegWithExif(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		img.Set(x, 0, color.RGBA{R: 255, A: 255})
		img.Set(x, 1, color.RGBA{B: 255, A: 255})
	}
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	raw := plain.Bytes()
	if raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatal("encoder did not start with SOI")
	}
	payload := exifWithGPS()
	var out bytes.Buffer
	out.Write(raw[:2])
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
	out.Write(payload)
	out.Write(raw[2:])
	return out.Bytes()
}

func TestStripOriginalMetadataDropsJPEGExifAndGPS(t *testing.T) {
	in := jpegWithExif(t)
	if !bytes.Contains(in, []byte("Exif")) {
		t.Fatal("fixture carries no Exif segment")
	}
	// The input decodes (sanity): the splice did not break the file.
	if _, _, err := image.Decode(bytes.NewReader(in)); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}

	out, err := StripOriginalMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.ContentType != "image/jpeg" || out.Format != "jpeg" {
		t.Fatalf("changed=%v type=%q format=%q", out.Changed, out.ContentType, out.Format)
	}
	if bytes.Contains(out.Bytes, []byte("Exif")) {
		t.Fatal("the stored original still carries an Exif segment (GPS, device, timestamps)")
	}
	// No APP1 marker at all.
	if bytes.Contains(out.Bytes, []byte{0xFF, 0xE1}) {
		t.Fatal("the stored original still carries an APP1 segment")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Bytes))
	if err != nil || format != "jpeg" {
		t.Fatalf("stored original decodes as %q: %v", format, err)
	}
	// Orientation 6 was applied before the tag was dropped: 4x2 became 2x4,
	// and the renditions are built from the same oriented pixels.
	if cfg.Width != 2 || cfg.Height != 4 {
		t.Fatalf("stored original is %dx%d, want 2x4 (EXIF orientation applied before the tag was removed)", cfg.Width, cfg.Height)
	}
	if b := out.Image.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("decoded image for renditions is %dx%d, want 2x4", b.Dx(), b.Dy())
	}
}

// pngChunk frames one PNG chunk.
func pngChunk(typ string, payload []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.WriteString(typ)
	b.Write(payload)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(payload)
	binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

func TestStripOriginalMetadataDropsPNGTextChunks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	img.Set(1, 1, color.RGBA{G: 200, A: 255})
	var plain bytes.Buffer
	if err := png.Encode(&plain, img); err != nil {
		t.Fatal(err)
	}
	raw := plain.Bytes()
	// Signature (8) + IHDR chunk (4+4+13+4 = 25): insert after it.
	const afterIHDR = 8 + 25
	secret := []byte("Comment\x00GPS 17.3961N 78.4716E taken on Pixel 9")
	in := append(append(append([]byte(nil), raw[:afterIHDR]...), pngChunk("tEXt", secret)...), raw[afterIHDR:]...)
	if _, _, err := image.Decode(bytes.NewReader(in)); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}

	out, err := StripOriginalMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.ContentType != "image/png" {
		t.Fatalf("changed=%v type=%q", out.Changed, out.ContentType)
	}
	if bytes.Contains(out.Bytes, []byte("tEXt")) || bytes.Contains(out.Bytes, []byte("Pixel 9")) {
		t.Fatal("the stored original still carries the text chunk")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Bytes))
	if err != nil || format != "png" || cfg.Width != 3 || cfg.Height != 3 {
		t.Fatalf("stored original: %q %dx%d %v", format, cfg.Width, cfg.Height, err)
	}
}

// webpChunk frames one RIFF chunk (padded to an even length).
func webpChunk(fourcc string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString(fourcc)
	binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
	b.Write(payload)
	if len(payload)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// minimalWebPVP8 is the 8x8 lossy bitstream from decoder_coverage_test.go
// (the VP8 chunk payload only).
var minimalWebPVP8 = []byte{
	0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x08, 0x00, 0x08, 0x00, 0x00, 0x47,
	0x08, 0x85, 0x85, 0x88, 0x85, 0x84, 0x88, 0x03, 0xf0, 0x00, 0xfe, 0xfb,
	0x94, 0x00, 0x00,
}

func TestStripOriginalMetadataDropsWebPExifAndXMP(t *testing.T) {
	// A VP8X container announcing EXIF and XMP. The walk is byte-level, so
	// it is tested on the container directly: the x/image decoder this
	// service pins refuses non-alpha VP8X files outright, and a lossy VP8
	// keyframe cannot be hand-written here.
	vp8x := []byte{webpFlagEXIF | webpFlagXMP, 0, 0, 0, 7, 0, 0, 7, 0, 0}
	exif := exifWithGPS()[6:] // TIFF only; WebP's EXIF chunk has no "Exif\0\0" prefix
	xmp := []byte("<x:xmpmeta>GPSLatitude=17.39</x:xmpmeta>")
	body := bytes.Join([][]byte{
		[]byte("WEBP"),
		webpChunk("VP8X", vp8x),
		webpChunk("EXIF", exif),
		webpChunk("VP8 ", minimalWebPVP8),
		webpChunk("XMP ", xmp),
	}, nil)
	var in bytes.Buffer
	in.WriteString("RIFF")
	binary.Write(&in, binary.LittleEndian, uint32(len(body)))
	in.Write(body)

	out, changed := stripWebPMetadata(in.Bytes())
	if !changed {
		t.Fatal("a WebP with EXIF and XMP chunks was reported clean")
	}
	for _, needle := range []string{"EXIF", "XMP ", "xmpmeta"} {
		if bytes.Contains(out, []byte(needle)) {
			t.Fatalf("the stored original still carries %q", needle)
		}
	}
	// The VP8X flags no longer announce EXIF/XMP, the RIFF size is the new
	// length, and the pixel chunk is byte-for-byte the input's.
	vp8xAt := bytes.Index(out, []byte("VP8X"))
	if vp8xAt < 0 || out[vp8xAt+8]&(webpFlagEXIF|webpFlagXMP) != 0 {
		t.Fatalf("VP8X flags still announce metadata: %v", out[vp8xAt+8])
	}
	if got := binary.LittleEndian.Uint32(out[4:8]); int(got) != len(out)-8 {
		t.Fatalf("RIFF size %d, want %d", got, len(out)-8)
	}
	if !bytes.Contains(out, webpChunk("VP8 ", minimalWebPVP8)) {
		t.Fatal("the pixel chunk was altered or dropped")
	}
	if want := in.Len() - len(webpChunk("EXIF", exif)) - len(webpChunk("XMP ", xmp)); len(out) != want {
		t.Fatalf("output length %d, want the input minus the two chunks (%d)", len(out), want)
	}
}

// A WebP with nothing to strip is stored as uploaded: no needless rewrite.
// (A 1x1 lossless VP8L file, which the pinned decoder reads fully.)
func TestStripOriginalMetadataLeavesCleanWebPAlone(t *testing.T) {
	in := []byte{
		0x52, 0x49, 0x46, 0x46, 0x1a, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
		0x56, 0x50, 0x38, 0x4c, 0x0d, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00,
		0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0x00,
	}
	out, err := StripOriginalMetadata(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed || !bytes.Equal(out.Bytes, in) || out.ContentType != "image/webp" {
		t.Fatalf("a clean WebP was rewritten: changed=%v type=%q", out.Changed, out.ContentType)
	}
	if b := out.Image.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("decoded %dx%d, want 1x1", b.Dx(), b.Dy())
	}
}

// memObjects is an in-memory imageObjectStore: what ProcessImage reads and
// what it leaves behind under each key.
type memObjects struct {
	objects map[string][]byte
	types   map[string]string
}

func (m *memObjects) DownloadObject(_ context.Context, key string) ([]byte, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("no object %q", key)
	}
	return append([]byte(nil), data...), nil
}

func (m *memObjects) UploadObject(_ context.Context, key string, data []byte, contentType string) error {
	m.objects[key] = append([]byte(nil), data...)
	m.types[key] = contentType
	return nil
}

// The worker path end to end: after ProcessImage the object under the
// ORIGINAL key — the one GetMediaURL signs as keys["original"] — no longer
// carries the camera's EXIF/GPS, and the size the row records is the size
// of what is now stored.
func TestProcessImageReplacesTheStoredOriginal(t *testing.T) {
	const key = "user/u1/m1/original/photo.jpg"
	store := &memObjects{objects: map[string][]byte{key: jpegWithExif(t)}, types: map[string]string{}}

	_, meta, err := ProcessImage(context.Background(), store, key, "m1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	stored := store.objects[key]
	if bytes.Contains(stored, []byte("Exif")) || bytes.Contains(stored, []byte{0xFF, 0xE1}) {
		t.Fatal("the stored original still carries the EXIF segment after processing")
	}
	if store.types[key] != "image/jpeg" {
		t.Fatalf("original re-stored as %q, want image/jpeg", store.types[key])
	}
	if meta.OriginalSizeBytes != int64(len(stored)) {
		t.Fatalf("meta size %d, stored %d: the row would answer HEAD with the old size", meta.OriginalSizeBytes, len(stored))
	}
}
