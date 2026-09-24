package processing

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

/*
Every image format the service ACCEPTS must be one it can DECODE.

validation.go accepts image/jpeg, image/png, image/gif and image/webp.
image.go registered only PNG and JPEG. So a WebP or a GIF passed validation,
was uploaded to storage, and then failed to decode: processing_status went to
"failed", /serve answered 404, and the page showed a broken image with nothing
anywhere explaining it.

That is exactly what happened to the founder's channel cover, which was a
WebP. It uploaded, it saved, the id was written to the channel — and the
banner never appeared.

A format accepted at the door and undecodable inside is worse than a format
refused at the door: the refusal is immediate and explains itself, while this
looks like success and fails silently minutes later.

image.RegisterFormat is global, so importing this package is what makes the
decoders available; these tests prove each registration is actually in place
rather than that the import line merely exists.

One honest limit, established by mutation: deleting the WebP import fails
TestWebPDecodes, but deleting the GIF import does NOT fail anything — the
imaging dependency already registers GIF transitively. So the GIF case here
records the requirement without proving the import earns its place. The
explicit import stays because this file should not depend on what a third
party happens to pull in, but do not read the GIF assertion as a guard.
*/

func TestAcceptedImageFormatsCanAllBeDecoded(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	src.Set(0, 0, color.RGBA{R: 200, G: 40, B: 40, A: 255})

	for _, tc := range []struct {
		name   string
		encode func() ([]byte, error)
	}{
		{"png", func() ([]byte, error) {
			var b bytes.Buffer
			if err := png.Encode(&b, src); err != nil {
				return nil, err
			}
			return b.Bytes(), nil
		}},
		{"jpeg", func() ([]byte, error) {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, src, nil); err != nil {
				return nil, err
			}
			return b.Bytes(), nil
		}},
		{"gif", func() ([]byte, error) {
			var b bytes.Buffer
			if err := gif.Encode(&b, src, nil); err != nil {
				return nil, err
			}
			return b.Bytes(), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.encode()
			if err != nil {
				t.Fatalf("encode %s: %v", tc.name, err)
			}
			if _, format, err := image.Decode(bytes.NewReader(data)); err != nil {
				t.Fatalf("%s is accepted by validation.go but cannot be decoded here — it will upload, fail processing, and serve a 404: %v", tc.name, err)
			} else if format != tc.name {
				t.Fatalf("decoded %s as %q", tc.name, format)
			}
		})
	}
}

/*
WebP has no encoder in the standard library or in x/image, so this decodes a
known-good 8x8 lossy WebP rather than round-tripping one. Bytes from the
libwebp test corpus, checked in as a literal so the test needs no fixture
file and no network.
*/
func TestWebPDecodes(t *testing.T) {
	// RIFF....WEBPVP8 — a minimal lossy WebP.
	webpBytes := []byte{
		0x52, 0x49, 0x46, 0x46, 0x2c, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
		0x56, 0x50, 0x38, 0x20, 0x20, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d,
		0x01, 0x2a, 0x08, 0x00, 0x08, 0x00, 0x00, 0x47, 0x08, 0x85, 0x85, 0x88,
		0x85, 0x84, 0x88, 0x03, 0xf0, 0x00, 0xfe, 0xfb, 0x94, 0x00, 0x00,
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(webpBytes))
	if err != nil {
		t.Fatalf("WebP is accepted by validation.go but no decoder is registered — every WebP upload will fail processing and serve a 404, which is what happened to the channel cover: %v", err)
	}
	if format != "webp" {
		t.Fatalf("decoded as %q, want webp", format)
	}
	if cfg.Width != 8 || cfg.Height != 8 {
		t.Fatalf("got %dx%d, want 8x8", cfg.Width, cfg.Height)
	}
}

