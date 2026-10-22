package processing

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
)

// Security sweep, 21 Oct 2026 (#4): image ORIGINALS were delivered with the
// phone's bytes intact. Every rendition is a fresh encode, but
// GetMediaURL signs keys["original"] = media.StorageKey for every image, so
// any viewer admitted to a post could download the author's camera file and
// read its GPS fix, device model and capture time. Metadata was stripped
// only for dating photos (dating_photo.go) and the KYC display route
// (display_image.go).
//
// StripOriginalMetadata is what the worker now runs on an image original
// BEFORE the renditions are built, and the result REPLACES the stored
// original under the same key — so the display pipeline (signed "original",
// HEAD from the rows, the anonymous stream) is untouched; only the bytes
// behind the key change:
//
//   - JPEG: decoded with the EXIF orientation applied and re-encoded by
//     image/jpeg, which writes no APPn or COM segment: EXIF (GPS, device,
//     timestamps), XMP, IPTC, ICC and comments are gone. Applying the
//     orientation first keeps a phone portrait upright once the tag that
//     said "rotate me" is no longer there (and the renditions, built from
//     the same oriented pixels, stop coming out sideways).
//   - PNG: re-encoded by image/png, which writes only IHDR, PLTE/tRNS, IDAT
//     and IEND, so eXIf, tEXt/iTXt/zTXt and every other ancillary chunk is
//     gone.
//   - WebP: Go has no encoder, so the RIFF container is rewritten without
//     its EXIF and "XMP " chunks (and the VP8X flags that announce them).
//     The pixel chunks are copied byte for byte: no generation loss.
//   - GIF: left as uploaded. GIF has no EXIF or XMP slot and re-encoding
//     would drop the animation; the first frame is still decoded for the
//     renditions.
//
// The decoded, oriented image is returned with the bytes so the worker does
// not decode the original twice.

// originalJPEGQuality is the quality a re-encoded JPEG original is written
// at. High enough that one generation is invisible, low enough that the
// object does not balloon.
const originalJPEGQuality = 92

// CleanOriginal is the outcome of StripOriginalMetadata.
type CleanOriginal struct {
	// Bytes is what the original object must hold from now on. Equal to the
	// input when Changed is false.
	Bytes []byte
	// ContentType is the MIME type of Bytes.
	ContentType string
	// Changed is true when Bytes differs from the input and the stored object
	// must be replaced.
	Changed bool
	// Image is the decoded original with its EXIF orientation applied — the
	// pixels every rendition is built from.
	Image image.Image
	// Format is the registered decoder name: "jpeg", "png", "gif" or "webp".
	Format string
}

// StripOriginalMetadata decodes data and returns the metadata-free bytes to
// store in its place (see the file comment for what each format keeps).
func StripOriginalMetadata(data []byte) (*CleanOriginal, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("strip metadata: empty image")
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}
	// AutoOrientation reads the EXIF orientation (JPEG only; a no-op for the
	// rest) and rotates the pixels, which is the one piece of metadata whose
	// meaning must survive the strip.
	src, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	out := &CleanOriginal{Image: src, Format: format, Bytes: data}
	switch format {
	case "jpeg":
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: originalJPEGQuality}); err != nil {
			return nil, fmt.Errorf("re-encode jpeg original: %w", err)
		}
		out.Bytes, out.ContentType, out.Changed = buf.Bytes(), "image/jpeg", true
	case "png":
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			return nil, fmt.Errorf("re-encode png original: %w", err)
		}
		out.Bytes, out.ContentType, out.Changed = buf.Bytes(), "image/png", true
	case "webp":
		clean, changed := stripWebPMetadata(data)
		out.Bytes, out.ContentType, out.Changed = clean, "image/webp", changed
	case "gif":
		out.ContentType = "image/gif"
	default:
		// A format the decoders know but this file does not: refuse rather
		// than store something whose metadata was never looked at.
		return nil, fmt.Errorf("strip metadata: no rule for %q", format)
	}
	return out, nil
}

// WebP extended-format (VP8X) flag bits (RFC 9649 §2.7).
const (
	webpFlagEXIF = 0x08
	webpFlagXMP  = 0x04
)

// stripWebPMetadata drops the EXIF and XMP chunks from a RIFF/WEBP container
// and clears their VP8X flags. Every other chunk (VP8X, ICCP, ANIM, ANMF,
// ALPH, VP8, VP8L) is copied unchanged. A container the walk cannot parse is
// returned as is with changed=false: the decoder accepted it, so the bytes
// are what they are, but nothing is claimed about them.
func stripWebPMetadata(data []byte) (out []byte, changed bool) {
	const header = 12
	if len(data) < header || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return data, false
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	end := 8 + riffSize
	if end > len(data) {
		end = len(data)
	}
	kept := make([]byte, 0, len(data))
	kept = append(kept, data[0:header]...)
	pos := header
	for pos+8 <= end {
		fourcc := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		padded := size + size%2
		if pos+8+padded > end {
			// Truncated chunk: cannot walk it; give the bytes back untouched.
			return data, false
		}
		chunk := data[pos : pos+8+padded]
		switch fourcc {
		case "EXIF", "XMP ":
			changed = true
		case "VP8X":
			if size >= 1 && chunk[8]&(webpFlagEXIF|webpFlagXMP) != 0 {
				c := append([]byte(nil), chunk...)
				c[8] &^= webpFlagEXIF | webpFlagXMP
				chunk = c
				changed = true
			}
			kept = append(kept, chunk...)
		default:
			kept = append(kept, chunk...)
		}
		pos += 8 + padded
	}
	if !changed {
		return data, false
	}
	binary.LittleEndian.PutUint32(kept[4:8], uint32(len(kept)-8))
	return kept, true
}
