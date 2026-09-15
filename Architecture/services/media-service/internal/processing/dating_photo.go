package processing

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/disintegration/imaging"
)

// Dating plan lane D6 — the pixels of a dating photo.
//
// PrepareDatingImage turns an uploaded image into what may be served:
//
//   - The original is decoded WITH its EXIF orientation applied and
//     re-encoded as a plain JPEG. Go's encoder writes no APPn or COM segment,
//     so EXIF (GPS, device, timestamps), XMP, IPTC and comments are gone.
//     Stripping the segments losslessly would also drop the orientation tag
//     and serve phone portraits sideways; re-encoding keeps the picture the
//     user saw.
//   - The standard renditions are rebuilt from the oriented pixels.
//   - The blurred variant is downscaled first and then blurred hard, so no
//     amount of client-side sharpening recovers a face: at 240px with a
//     sigma of 14 the detail is not in the bytes.

// DatingBlurVariant is the rendition name of a dating photo's blurred image.
const DatingBlurVariant = "dating_blurred"

const (
	datingBlurMaxSize     = 240
	datingBlurSigma       = 14.0
	datingBlurQuality     = 70
	datingOriginalQuality = 90
	// MaxDatingImagePixels refuses a decompression bomb before decoding
	// allocates it.
	MaxDatingImagePixels = 50_000_000
)

// ErrDatingImageUnsupported: the bytes are not an image this service decodes.
var ErrDatingImageUnsupported = errors.New("dating image: unsupported or unreadable image")

// RenderedImage is one encoded JPEG.
type RenderedImage struct {
	Name   string
	Bytes  []byte
	Width  int
	Height int
}

// DatingImage is everything PrepareDatingImage renders.
type DatingImage struct {
	Original RenderedImage
	Variants []RenderedImage
	Blurred  RenderedImage
}

// DatingImageStamper may add test data to a prepared image before it is
// stored. Only MockFaceComparer implements it (local/dev; the mock is refused
// elsewhere). The dating photo service calls it only when the wired face
// counter implements it, so with Rekognition the prepared bytes are exactly
// what PrepareDatingImage rendered.
type DatingImageStamper interface {
	StampPreparedDatingImage(uploaded []byte, img *DatingImage)
}

// PrepareDatingImage renders the metadata-free original, the rendition
// ladder and the blurred variant.
func PrepareDatingImage(data []byte) (*DatingImage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrDatingImageUnsupported)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDatingImageUnsupported, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxDatingImagePixels {
		return nil, fmt.Errorf("%w: %dx%d", ErrDatingImageUnsupported, cfg.Width, cfg.Height)
	}
	src, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDatingImageUnsupported, err)
	}

	out := &DatingImage{}
	if out.Original, err = encodeDatingJPEG("original", src, datingOriginalQuality); err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	for _, spec := range DefaultImageVariants {
		var resized image.Image
		if spec.Crop {
			resized = imaging.Fill(src, spec.MaxSize, spec.MaxSize, imaging.Center, imaging.Lanczos)
		} else {
			// Same rule as ProcessImage: no rendition larger than the source.
			if w <= spec.MaxSize && h <= spec.MaxSize {
				continue
			}
			resized = imaging.Fit(src, spec.MaxSize, spec.MaxSize, imaging.Lanczos)
		}
		v, err := encodeDatingJPEG(spec.Name, resized, spec.Quality)
		if err != nil {
			return nil, err
		}
		out.Variants = append(out.Variants, v)
	}
	small := imaging.Fit(src, datingBlurMaxSize, datingBlurMaxSize, imaging.Linear)
	if out.Blurred, err = encodeDatingJPEG(DatingBlurVariant, imaging.Blur(small, datingBlurSigma), datingBlurQuality); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeDatingJPEG(name string, img image.Image, quality int) (RenderedImage, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return RenderedImage{}, fmt.Errorf("encode %s: %w", name, err)
	}
	bounds := img.Bounds()
	return RenderedImage{Name: name, Bytes: buf.Bytes(), Width: bounds.Dx(), Height: bounds.Dy()}, nil
}

// JPEGHasMetadata reports whether a JPEG carries an APP1-APP15 segment
// (EXIF/GPS, XMP, ICC, IPTC...) or a comment before its image data. Not a
// JPEG, or a truncated one, reports false.
func JPEGHasMetadata(data []byte) bool {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return false
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return false
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0xDA || marker == 0xD9: // start of scan / end of image
			return false
		case (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01: // no length
			i += 2
			continue
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 {
			return false
		}
		if (marker >= 0xE1 && marker <= 0xEF) || marker == 0xFE {
			return true
		}
		i += 2 + size
	}
	return false
}
