package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
)

// Gate line 6 sweep (2026-11-18): the routes that run a decoder or a tool on
// user bytes in the API process are bounded before they do.

// pngHeader is a valid PNG signature and IHDR declaring width × height; the
// pixels never follow, which is all DecodeConfig needs.
func pngHeader(width, height uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

type fakeImageObjects struct {
	data       []byte
	downloaded bool
}

func (f *fakeImageObjects) ReadObjectRange(_ context.Context, _ string, start, end int64) ([]byte, error) {
	if end >= int64(len(f.data)) {
		end = int64(len(f.data)) - 1
	}
	return f.data[start : end+1], nil
}

func (f *fakeImageObjects) DownloadObject(context.Context, string) ([]byte, error) {
	f.downloaded = true
	return f.data, nil
}

func TestConfirmRefusesADecompressionBombBeforeDecoding(t *testing.T) {
	bomb := pngHeader(60000, 60000)
	objs := &fakeImageObjects{data: bomb}
	err := checkImagePixels(context.Background(), objs, &postgres.MediaAsset{StorageKey: "k", FileSizeBytes: int64(len(bomb))})
	if !errors.Is(err, ErrImageTooManyPixels) {
		t.Fatalf("err %v, want ErrImageTooManyPixels", err)
	}
	if objs.downloaded {
		t.Fatal("a PNG's size is in its header: the whole file must not be read")
	}
}

func TestConfirmAdmitsAnOrdinaryPhoto(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	img.Set(1, 1, color.White)
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	objs := &fakeImageObjects{data: buf.Bytes()}
	if err := checkImagePixels(context.Background(), objs, &postgres.MediaAsset{StorageKey: "k", FileSizeBytes: int64(buf.Len())}); err != nil {
		t.Fatalf("40x30 PNG refused: %v", err)
	}
}

func TestImagePixelsAllowedBoundary(t *testing.T) {
	if err := imagePixelsAllowed(8064, 6048); err != nil { // a 48 MP phone photo
		t.Fatalf("48 MP refused: %v", err)
	}
	if err := imagePixelsAllowed(int(MaxConfirmImagePixels), 1); err != nil {
		t.Fatalf("exactly the cap refused: %v", err)
	}
	for _, wh := range [][2]int{{int(MaxConfirmImagePixels) + 1, 1}, {0, 10}, {10, -1}} {
		if err := imagePixelsAllowed(wh[0], wh[1]); !errors.Is(err, ErrImageTooManyPixels) {
			t.Fatalf("%dx%d: err %v", wh[0], wh[1], err)
		}
	}
}

func TestFrameSourceCopyIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input")

	// The object store's size over the cap: refused before copying.
	if err := copyBounded(strings.NewReader("x"), 11, path, 10); !errors.Is(err, ErrFrameSourceTooLarge) {
		t.Fatalf("declared over: %v", err)
	}
	// A stream longer than it said: refused, and nothing left behind.
	if err := copyBounded(strings.NewReader(strings.Repeat("x", 11)), 5, path, 10); !errors.Is(err, ErrFrameSourceTooLarge) {
		t.Fatalf("stream over: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused copy left %s behind (%v)", path, err)
	}
	// At the cap: copied whole.
	if err := copyBounded(strings.NewReader(strings.Repeat("x", 10)), 10, path, 10); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != 10 {
		t.Fatalf("copied %v %v", fi, err)
	}
}
