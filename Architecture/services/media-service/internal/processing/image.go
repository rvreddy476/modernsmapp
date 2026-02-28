package processing

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"strings"

	"github.com/buckket/go-blurhash"
	"github.com/disintegration/imaging"
	"github.com/facebook-like/media-service/internal/store/blob"
)

// VariantSpec defines a target image variant.
type VariantSpec struct {
	Name    string
	MaxSize int  // max width or height (0 = crop to exact)
	Crop    bool // center-crop to exact square
	Quality int  // JPEG quality 1-100
}

// DefaultImageVariants are the standard resize targets.
var DefaultImageVariants = []VariantSpec{
	{Name: "thumb_150", MaxSize: 150, Crop: true, Quality: 80},
	{Name: "small_480", MaxSize: 480, Crop: false, Quality: 85},
	{Name: "medium_1080", MaxSize: 1080, Crop: false, Quality: 90},
}

// ImageResult holds metadata from processing.
type ImageResult struct {
	Width    int
	Height   int
	Blurhash string
}

// VariantOutput holds the result of a single variant generation.
type VariantOutput struct {
	Name      string
	Width     int
	Height    int
	SizeBytes int64
	ObjectKey string
	Mime      string
}

// ProcessImage downloads the original, generates variants, and uploads them.
func ProcessImage(ctx context.Context, blobStore *blob.Store, objectKey string, mediaIDStr string, ownerIDStr string) ([]VariantOutput, *ImageResult, error) {
	// 1. Download original
	data, err := blobStore.DownloadObject(ctx, objectKey)
	if err != nil {
		return nil, nil, fmt.Errorf("download original: %w", err)
	}

	// 2. Decode image
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("decode image: %w", err)
	}

	bounds := src.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	// Generate blurhash (4x3 components, standard for social media)
	hash, err := blurhash.Encode(4, 3, src)
	if err != nil {
		hash = "" // non-fatal: continue without blurhash
	}

	meta := &ImageResult{
		Width:    origWidth,
		Height:   origHeight,
		Blurhash: hash,
	}

	// 3. Generate variants
	var outputs []VariantOutput
	for _, spec := range DefaultImageVariants {
		var resized image.Image
		if spec.Crop {
			resized = imaging.Fill(src, spec.MaxSize, spec.MaxSize, imaging.Center, imaging.Lanczos)
		} else {
			// Skip variant if original is smaller than target
			if origWidth <= spec.MaxSize && origHeight <= spec.MaxSize {
				continue
			}
			resized = imaging.Fit(src, spec.MaxSize, spec.MaxSize, imaging.Lanczos)
		}

		// Encode to JPEG
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: spec.Quality}); err != nil {
			return nil, nil, fmt.Errorf("encode variant %s: %w", spec.Name, err)
		}

		// Build object key: replace /original with /variant_name
		variantKey := strings.Replace(objectKey, "/original", "/"+spec.Name, 1)

		// Upload to MinIO
		if err := blobStore.UploadObject(ctx, variantKey, buf.Bytes(), "image/jpeg"); err != nil {
			return nil, nil, fmt.Errorf("upload variant %s: %w", spec.Name, err)
		}

		rBounds := resized.Bounds()
		w := rBounds.Dx()
		h := rBounds.Dy()
		sz := int64(buf.Len())

		outputs = append(outputs, VariantOutput{
			Name:      spec.Name,
			Width:     w,
			Height:    h,
			SizeBytes: sz,
			ObjectKey: variantKey,
			Mime:      "image/jpeg",
		})
	}

	return outputs, meta, nil
}
