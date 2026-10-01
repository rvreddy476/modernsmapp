package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Seller KYC documents, view-only (1 Oct 2026).
//
// ImageBytesReader backs GET /v1/media/internal/:mediaId/image-bytes: the
// bytes of one ready image, rendered metadata-free (processing.
// RenderDisplayImage), for commerce-service to stream to the admin console.
// It never returns a URL and writes nothing.
//
// Only an image asset whose processing is "ready" and whose moderation did
// not reject it is served. Every other case — no such row, a video, an
// upload still processing or failed, a deleted asset, bytes that are gone —
// is ErrImageBytesNotFound, one answer, so the route never says why.

// ErrImageBytesNotFound is the single not-found answer of the reader.
var ErrImageBytesNotFound = errors.New("media: image not available")

// imageBytesRecords is the slice of the media store the reader needs.
type imageBytesRecords interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaVariant, error)
}

// imageBytesObjects is the slice of the object store the reader needs.
type imageBytesObjects interface {
	OpenObject(ctx context.Context, objectKey string) (io.ReadCloser, blob.ObjectInfo, error)
}

// ImageBytesReader renders the display image of one asset.
type ImageBytesReader struct {
	records imageBytesRecords
	objects imageBytesObjects
	// maxBytes caps the rendered image; maxSource caps what is read of a
	// stored object. Tests lower them.
	maxBytes  int
	maxSource int64
}

// NewImageBytesReader builds a reader over a record store and object store.
func NewImageBytesReader(records imageBytesRecords, objects imageBytesObjects) *ImageBytesReader {
	return &ImageBytesReader{
		records:   records,
		objects:   objects,
		maxBytes:  processing.DisplayImageMaxBytes,
		maxSource: processing.DisplayImageMaxSourceBytes,
	}
}

// WithLimits overrides the caps (tests).
func (r *ImageBytesReader) WithLimits(maxBytes int, maxSource int64) *ImageBytesReader {
	r.maxBytes, r.maxSource = maxBytes, maxSource
	return r
}

// ImageBytesReader returns the reader over this service's stores.
func (s *Service) ImageBytesReader() *ImageBytesReader {
	return NewImageBytesReader(s.pgStore, s.blobStore)
}

// displaySourceVariants are the worker renditions tried, in order, when the
// original cannot be rendered. thumb_150 is a centre crop and never a
// faithful view of a document, so it is not a source.
var displaySourceVariants = []string{"medium_1080", "small_480"}

// DisplayImage returns the metadata-free display image of mediaID.
//
// The ORIGINAL is the preferred source: rendered with its orientation applied
// and bounded to processing.DisplayImageMaxEdge. A worker rendition is used
// only when the original object is missing or undecodable; it is re-encoded
// the same way, so no stored byte is ever passed through as-is.
func (r *ImageBytesReader) DisplayImage(ctx context.Context, mediaID uuid.UUID) (*processing.DisplayImage, error) {
	m, err := r.records.GetMedia(ctx, mediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrImageBytesNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("image bytes: read media: %w", err)
	}
	if m == nil || m.FileType != "image" || m.ProcessingStatus != "ready" || m.ModerationStatus == "rejected" {
		return nil, ErrImageBytesNotFound
	}

	keys := []string{m.StorageKey}
	variants, err := r.records.GetVariants(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("image bytes: read variants: %w", err)
	}
	for _, name := range displaySourceVariants {
		for _, v := range variants {
			if v.Name == name && v.ObjectKey != "" {
				keys = append(keys, v.ObjectKey)
			}
		}
	}

	for _, key := range keys {
		if key == "" {
			continue
		}
		img, err := r.render(ctx, key)
		if err == nil {
			return img, nil
		}
		if errors.Is(err, blob.ErrObjectNotFound) ||
			errors.Is(err, processing.ErrDisplayImageUnsupported) ||
			errors.Is(err, processing.ErrDisplayImageTooLarge) {
			continue
		}
		// The object store failed rather than answering "no such object":
		// that is an outage, not a missing image.
		return nil, err
	}
	return nil, ErrImageBytesNotFound
}

func (r *ImageBytesReader) render(ctx context.Context, key string) (*processing.DisplayImage, error) {
	rc, info, err := r.objects.OpenObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Refuse an object the store says is too large before reading any of it.
	if info.Size > r.maxSource {
		return nil, fmt.Errorf("%w: source of %d bytes over %d", processing.ErrDisplayImageUnsupported, info.Size, r.maxSource)
	}
	data, err := io.ReadAll(io.LimitReader(rc, r.maxSource+1))
	if err != nil {
		return nil, fmt.Errorf("image bytes: read object: %w", err)
	}
	// The stored size can be absent or stale: never render a truncated read.
	if int64(len(data)) > r.maxSource {
		return nil, fmt.Errorf("%w: source over %d bytes", processing.ErrDisplayImageUnsupported, r.maxSource)
	}
	return processing.RenderDisplayImage(data, r.maxBytes)
}
