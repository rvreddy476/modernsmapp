package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
)

// Decompression bombs on the confirm path (gate line 6 sweep, 2026-11-18).
//
// Every confirmed image is decoded in the API process (processImage →
// processing.ProcessImage → StripOriginalMetadata). The size cap bounds the
// FILE (20 MB), not the PIXELS: a 20 MB PNG of one colour can declare
// 60 000 × 60 000, which decodes to ~14 GB of RGBA and kills the pod. The
// dating, display and KYC paths already refuse more than
// processing.MaxDatingImagePixels before decoding; the general confirm path
// did not. This reads only the header (DecodeConfig) and refuses first.

// MaxConfirmImagePixels is the largest image a confirm decodes: 50 MP, the
// cap the dating and display paths use. A 48 MP phone photo fits.
const MaxConfirmImagePixels = processing.MaxDatingImagePixels

// imageHeaderPrefixBytes is how much of the object the header check reads
// first. PNG, GIF and WebP declare their size in the first bytes; a JPEG's
// SOF can follow large APP segments (EXIF thumbnail, ICC profile), so a
// prefix that does not reach it falls back to the whole (size-capped) file.
const imageHeaderPrefixBytes = 512 * 1024

// ErrImageTooManyPixels: the image declares more pixels than a confirm
// will decode. The asset is marked failed like any other unprocessable
// image.
var ErrImageTooManyPixels = errors.New("image dimensions exceed the pixel limit")

type imageHeaderReader interface {
	ReadObjectRange(ctx context.Context, objectKey string, start, end int64) ([]byte, error)
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
}

// checkImagePixels refuses an image whose header declares more than
// MaxConfirmImagePixels, before anything decodes its pixels.
func checkImagePixels(ctx context.Context, blobs imageHeaderReader, media *postgres.MediaAsset) error {
	end := int64(imageHeaderPrefixBytes) - 1
	if media.FileSizeBytes > 0 && media.FileSizeBytes-1 < end {
		end = media.FileSizeBytes - 1
	}
	head, err := blobs.ReadObjectRange(ctx, media.StorageKey, 0, end)
	if err != nil {
		return fmt.Errorf("read image header: %w", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		// The header is past the prefix (or the file is not an image, which
		// the decode below would refuse anyway): read the whole file, whose
		// size confirm has already checked against the declared, capped one.
		whole, dlErr := blobs.DownloadObject(ctx, media.StorageKey)
		if dlErr != nil {
			return fmt.Errorf("read image: %w", dlErr)
		}
		cfg, _, err = image.DecodeConfig(bytes.NewReader(whole))
		if err != nil {
			return fmt.Errorf("decode image config: %w", err)
		}
	}
	return imagePixelsAllowed(cfg.Width, cfg.Height)
}

// imagePixelsAllowed: both sides positive and the area at most
// MaxConfirmImagePixels.
func imagePixelsAllowed(width, height int) error {
	if width <= 0 || height <= 0 || int64(width)*int64(height) > MaxConfirmImagePixels {
		return fmt.Errorf("%w: %dx%d (max %d pixels)", ErrImageTooManyPixels, width, height, MaxConfirmImagePixels)
	}
	return nil
}
