package processing

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
)

// Seller KYC documents, view-only (1 Oct 2026) — the pixels an admin sees.
//
// RenderDisplayImage turns stored image bytes into what the internal
// image-bytes route may hand to commerce-service (and, through it and
// admin-service, to the admin console's canvas). Every byte it returns is a
// FRESH encode by Go's own encoders:
//
//   - image/jpeg writes no APPn or COM segment, so EXIF (GPS, device,
//     timestamps), XMP, IPTC, ICC and comments in the upload are gone.
//   - image/png writes only IHDR, PLTE/tRNS, IDAT and IEND, so a PNG's
//     eXIf, tEXt/iTXt/zTXt and other ancillary chunks are gone too.
//
// The EXIF orientation is applied before the metadata is dropped, so a
// document photographed on a phone reads the right way up (the worker's
// renditions are built from image.Decode, which ignores the orientation tag,
// and would show such a photo sideways). The long edge is capped at
// DisplayImageMaxEdge: legible for an identity document, never a full-size
// copy of the camera file.
//
// PNG stays PNG (lossless scans and screenshots of documents keep their
// edges); everything else becomes JPEG over a white background, so a
// transparent WebP does not turn black.

const (
	// DisplayImageMaxEdge caps the longer side of the rendered image.
	DisplayImageMaxEdge = 2048
	// DisplayImageMaxBytes is the most the route will ever send (15 MB).
	DisplayImageMaxBytes = 15 << 20
	// DisplayImageMaxSourceBytes is the most of a stored object that is read
	// into memory to render it.
	DisplayImageMaxSourceBytes = 50 << 20
	displayImageQuality        = 90
	displayImageFallbackQ      = 75
)

var (
	// ErrDisplayImageUnsupported: the bytes are not an image this service
	// decodes, or are larger than it will decode.
	ErrDisplayImageUnsupported = errors.New("display image: unsupported or unreadable image")
	// ErrDisplayImageTooLarge: even the fallback encode exceeds the cap.
	ErrDisplayImageTooLarge = errors.New("display image: rendered image exceeds the size cap")
)

// DisplayImage is one rendered, metadata-free image.
type DisplayImage struct {
	Bytes       []byte
	ContentType string
	Width       int
	Height      int
}

// RenderDisplayImage decodes data (orientation applied), bounds it to
// DisplayImageMaxEdge and re-encodes it, refusing a result over maxBytes.
func RenderDisplayImage(data []byte, maxBytes int) (*DisplayImage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrDisplayImageUnsupported)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDisplayImageUnsupported, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxDatingImagePixels {
		// Refuse a decompression bomb before decoding allocates it.
		return nil, fmt.Errorf("%w: %dx%d", ErrDisplayImageUnsupported, cfg.Width, cfg.Height)
	}
	src, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDisplayImageUnsupported, err)
	}
	b := src.Bounds()
	if b.Dx() > DisplayImageMaxEdge || b.Dy() > DisplayImageMaxEdge {
		src = imaging.Fit(src, DisplayImageMaxEdge, DisplayImageMaxEdge, imaging.Lanczos)
	}
	b = src.Bounds()
	out := &DisplayImage{Width: b.Dx(), Height: b.Dy()}

	if format == "png" {
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
		if buf.Len() <= maxBytes {
			out.Bytes, out.ContentType = buf.Bytes(), "image/png"
			return out, nil
		}
		// A noisy PNG can outgrow the cap; fall through to JPEG.
	}

	flat := imaging.New(b.Dx(), b.Dy(), color.White)
	flat = imaging.Overlay(flat, src, image.Pt(0, 0), 1.0)
	for _, q := range []int{displayImageQuality, displayImageFallbackQ} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
		if buf.Len() <= maxBytes {
			out.Bytes, out.ContentType = buf.Bytes(), "image/jpeg"
			return out, nil
		}
	}
	return nil, ErrDisplayImageTooLarge
}
